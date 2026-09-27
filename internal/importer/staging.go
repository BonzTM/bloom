package importer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	stagingDirectory         = "imports"
	stagingSuffix            = ".jsonl"
	partialSuffix            = ".partial"
	maxStagingSweepEntries   = core.MaxActiveImportUploads + 1
	stagingSweepChunkSize    = 128
	uploadUnavailableMessage = "This Bloom cannot store uploads; set BLOOM_DATA_DIR to a writable directory."
)

var errStagingUnavailable = errors.New("import staging unavailable")

// Staging confines uploaded import files to one private local root.
type Staging struct {
	root            string
	slots           chan struct{}
	maxBytes        int64
	transferTimeout time.Duration
	mu              sync.RWMutex
	reserved        map[string]struct{}
	unavailable     error
	established     bool
}

// NewStaging resolves the private import root and records setup failures for lazy recovery.
func NewStaging(dataDirectory string, transferTimeout time.Duration) (*Staging, error) {
	if dataDirectory == "" || transferTimeout <= 0 {
		return nil, fmt.Errorf("create import staging: %w", core.ErrInvalidArgument)
	}
	root, err := filepath.Abs(filepath.Join(dataDirectory, stagingDirectory))
	if err != nil {
		return nil, fmt.Errorf("resolve import staging root: %w", err)
	}
	staging := &Staging{
		root: root, slots: make(chan struct{}, 1), maxBytes: core.MaxImportUploadBytes,
		transferTimeout: transferTimeout, reserved: make(map[string]struct{}),
	}
	staging.unavailable = prepareStagingRoot(root)
	staging.established = staging.unavailable == nil
	return staging, nil
}

func prepareStagingRoot(root string) error {
	if mkdirErr := os.MkdirAll(root, 0o700); mkdirErr != nil {
		return fmt.Errorf("create import staging root: %w", mkdirErr)
	}
	return secureStagingRoot(root)
}

func secureStagingRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("inspect import staging root: %w", errors.Join(err, core.ErrInvalidArgument))
	}
	if err := os.Chmod(root, 0o700); err != nil { //nolint:gosec // Directories require owner execute permission.
		return fmt.Errorf("secure import staging root: %w", err)
	}
	return nil
}

// UnavailableCause reports why the staging root could not be prepared.
func (s *Staging) UnavailableCause() error {
	if s == nil {
		return core.ErrInvalidArgument
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unavailable
}

func (s *Staging) isUnavailable() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unavailable != nil
}

func (s *Staging) ensureAvailable() error {
	if s == nil {
		return core.ErrInvalidArgument
	}
	if err := s.refreshAvailability(); err != nil {
		return uploadUnavailableError()
	}
	return nil
}

func (s *Staging) refreshAvailability() error {
	if s == nil {
		return core.ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.established {
		s.unavailable = secureStagingRoot(s.root)
	} else {
		s.unavailable = prepareStagingRoot(s.root)
	}
	if s.unavailable != nil {
		return errors.Join(errStagingUnavailable, s.unavailable)
	}
	s.established = true
	return nil
}

func (s *Staging) checkAvailability() error {
	if s == nil {
		return core.ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unavailable = secureStagingRoot(s.root)
	if s.unavailable != nil {
		return errors.Join(errStagingUnavailable, s.unavailable)
	}
	s.established = true
	return nil
}

func (s *Staging) markUnavailable(cause error) error {
	s.mu.Lock()
	s.unavailable = cause
	s.mu.Unlock()
	return errors.Join(errStagingUnavailable, cause)
}

func uploadUnavailableError() error {
	return &core.InvalidArgumentError{
		Field: "source", Code: "unavailable", Message: uploadUnavailableMessage,
	}
}

func (s *Staging) stage(upload io.Reader) (id string, result error) {
	if s == nil || upload == nil {
		return "", core.ErrInvalidArgument
	}
	id, err := core.NewID()
	if err != nil {
		return "", fmt.Errorf("create staging identifier: %w", err)
	}
	reservedID := id
	s.reserve(reservedID)
	defer func() {
		if result != nil {
			s.release(reservedID)
		}
	}()
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return "", s.markUnavailable(fmt.Errorf("open import staging root: %w", err))
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	partial := id + partialSuffix
	file, err := root.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", s.markUnavailable(fmt.Errorf("create import staging file: %w", err))
	}
	written, copyErr := io.Copy(file, io.LimitReader(upload, s.maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > s.maxBytes {
		removeErr := removeStagingPartial(root, partial)
		if copyErr == nil && closeErr == nil {
			return "", errors.Join(fmt.Errorf("upload exceeds size limit: %w", core.ErrInvalidArgument), removeErr)
		}
		return "", errors.Join(copyErr, closeErr, removeErr)
	}
	if err := root.Rename(partial, stagingName(id)); err != nil {
		cause := errors.Join(fmt.Errorf("publish import staging file: %w", err), removeStagingPartial(root, partial))
		return "", s.markUnavailable(cause)
	}
	return id, nil
}

func (s *Staging) reserve(id string) {
	s.mu.Lock()
	s.reserved[id] = struct{}{}
	s.mu.Unlock()
}

func (s *Staging) release(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.reserved, id)
	s.mu.Unlock()
}

func (s *Staging) isReserved(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, reserved := s.reserved[id]
	return reserved
}

func removeStagingPartial(root *os.Root, name string) error {
	err := root.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Staging) open(id string) (*os.File, error) {
	root, name, err := s.openRootFor(id, stagingSuffix)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return nil, s.classifyOpenError("inspect import staging file", err)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, s.classifyOpenError("open import staging file", err)
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, s.classifyOpenError("verify import staging file", err)
	}
	return file, nil
}

func (s *Staging) classifyOpenError(operation string, cause error) error {
	info, rootErr := os.Lstat(s.root)
	if rootErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		unavailable := s.markUnavailable(errors.Join(rootErr, core.ErrInvalidArgument))
		return fmt.Errorf("%s: %w", operation, errors.Join(cause, unavailable))
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(cause, core.ErrInvalidArgument))
}

func (s *Staging) remove(id string) error {
	return s.removeNamed(id, stagingSuffix)
}

func (s *Staging) removeNamed(id, suffix string) error {
	root, name, err := s.openRootFor(id, suffix)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || info.IsDir() {
		return fmt.Errorf("inspect import staging removal: %w", errors.Join(err, core.ErrInvalidArgument))
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove import staging file: %w", err)
	}
	return nil
}

func (s *Staging) sweep(active map[string]struct{}) (result error) {
	if s.isUnavailable() {
		return nil
	}
	directory, err := os.Open(s.root)
	if err != nil {
		return s.markUnavailable(fmt.Errorf("open import staging root: %w", err))
	}
	defer func() { result = errors.Join(result, directory.Close()) }()
	processed := 0
	for processed < maxStagingSweepEntries {
		limit := min(stagingSweepChunkSize, maxStagingSweepEntries-processed)
		entries, readErr := directory.ReadDir(limit)
		for _, entry := range entries {
			if err := s.sweepEntry(active, entry); err != nil {
				return err
			}
		}
		processed += len(entries)
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read import staging root: %w", readErr)
		}
	}
	return nil
}

func (s *Staging) sweepEntry(active map[string]struct{}, entry fs.DirEntry) error {
	name := entry.Name()
	id, suffix, ok := stagingEntry(name)
	if !ok {
		return nil
	}
	if _, referenced := active[id]; referenced && suffix == stagingSuffix {
		return nil
	}
	if s.isReserved(id) {
		return nil
	}
	info, err := entry.Info()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect import staging entry: %w", err)
	}
	if time.Since(info.ModTime()) < s.transferTimeout {
		return nil
	}
	return s.removeNamed(id, suffix)
}

func (s *Staging) openRootFor(id, suffix string) (*os.Root, string, error) {
	if s == nil || !core.ValidID(id) || (suffix != stagingSuffix && suffix != partialSuffix) {
		return nil, "", core.ErrInvalidArgument
	}
	name := id + suffix
	path := filepath.Join(s.root, name)
	relative, err := filepath.Rel(s.root, path)
	if err != nil || relative != name || strings.Contains(relative, string(filepath.Separator)) {
		return nil, "", fmt.Errorf("resolve import staging file: %w", core.ErrInvalidArgument)
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, "", s.markUnavailable(fmt.Errorf("open import staging root: %w", err))
	}
	return root, name, nil
}

func stagingName(id string) string { return id + stagingSuffix }

func stagingID(name string) (string, bool) {
	if !strings.HasSuffix(name, stagingSuffix) {
		return "", false
	}
	id := strings.TrimSuffix(name, stagingSuffix)
	return id, core.ValidID(id)
}

func stagingEntry(name string) (string, string, bool) {
	if id, ok := stagingID(name); ok {
		return id, stagingSuffix, true
	}
	if !strings.HasSuffix(name, partialSuffix) {
		return "", "", false
	}
	id := strings.TrimSuffix(name, partialSuffix)
	return id, partialSuffix, core.ValidID(id)
}
