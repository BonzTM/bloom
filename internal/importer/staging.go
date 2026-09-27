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

	"github.com/BonzTM/bloom/internal/core"
)

const (
	stagingDirectory         = "imports"
	stagingSuffix            = ".jsonl"
	partialSuffix            = ".partial"
	maxStagingFiles          = core.MaxActiveImportUploads + 1
	uploadUnavailableMessage = "This Bloom cannot store uploads; set BLOOM_DATA_DIR to a writable directory."
)

// Staging confines uploaded import files to one private local root.
type Staging struct {
	root        string
	slots       chan struct{}
	maxBytes    int64
	mu          sync.RWMutex
	unavailable error
}

// NewStaging resolves the private import root and records setup failures for lazy recovery.
func NewStaging(dataDirectory string) (*Staging, error) {
	if dataDirectory == "" {
		return nil, fmt.Errorf("create import staging: %w", core.ErrInvalidArgument)
	}
	root, err := filepath.Abs(filepath.Join(dataDirectory, stagingDirectory))
	if err != nil {
		return nil, fmt.Errorf("resolve import staging root: %w", err)
	}
	staging := &Staging{root: root, slots: make(chan struct{}, 1), maxBytes: core.MaxImportUploadBytes}
	staging.unavailable = prepareStagingRoot(root)
	return staging, nil
}

func prepareStagingRoot(root string) error {
	if mkdirErr := os.MkdirAll(root, 0o700); mkdirErr != nil {
		return fmt.Errorf("create import staging root: %w", mkdirErr)
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unavailable == nil {
		return nil
	}
	s.unavailable = prepareStagingRoot(s.root)
	if s.unavailable != nil {
		return uploadUnavailableError()
	}
	return nil
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
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return "", fmt.Errorf("open import staging root: %w", err)
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	partial := id + partialSuffix
	file, err := root.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create import staging file: %w", err)
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
		return "", errors.Join(fmt.Errorf("publish import staging file: %w", err), removeStagingPartial(root, partial))
	}
	return id, nil
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
		return nil, fmt.Errorf("inspect import staging file: %w", errors.Join(err, core.ErrInvalidArgument))
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open import staging file: %w", err)
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, fmt.Errorf("verify import staging file: %w", errors.Join(err, core.ErrInvalidArgument))
	}
	return file, nil
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

func (s *Staging) sweep(active map[string]struct{}) error {
	if s.isUnavailable() {
		return nil
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return fmt.Errorf("read import staging root: %w", err)
	}
	if len(entries) > maxStagingFiles {
		return errors.New("import staging file count exceeds safety bound")
	}
	for _, entry := range entries {
		id, suffix, ok := stagingEntry(entry.Name())
		if !ok {
			continue
		}
		if _, referenced := active[id]; referenced && suffix == stagingSuffix {
			continue
		}
		if err := s.removeNamed(id, suffix); err != nil {
			return err
		}
	}
	return nil
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
		return nil, "", fmt.Errorf("open import staging root: %w", err)
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
