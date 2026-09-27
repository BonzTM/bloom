package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type uploadStore interface {
	WriteImportUploadChunks(context.Context, []core.ImportUploadChunk) error
	ImportUploadInfo(context.Context, string) (core.ImportUploadInfo, error)
	ReadImportUploadChunk(context.Context, string, int64) ([]byte, error)
	DeleteImportUpload(context.Context, string) error
	DeleteOrphanImportUploads(context.Context, time.Time, int) (int64, error)
}

// Staging streams bounded uploads into fixed-size database chunks.
type Staging struct {
	store           uploadStore
	slots           chan struct{}
	maxBytes        int64
	transferTimeout time.Duration
}

// NewStaging constructs database-backed import staging.
func NewStaging(store uploadStore, transferTimeout time.Duration) (*Staging, error) {
	if store == nil || transferTimeout <= 0 {
		return nil, fmt.Errorf("create import staging: %w", core.ErrInvalidArgument)
	}
	return &Staging{
		store: store, slots: make(chan struct{}, 1), maxBytes: core.MaxImportUploadBytes,
		transferTimeout: transferTimeout,
	}, nil
}

func (s *Staging) stage(ctx context.Context, upload io.Reader, clock core.Clock) (id string, result error) {
	if s == nil || upload == nil || clock == nil || clock.Now().IsZero() {
		return "", core.ErrInvalidArgument
	}
	id, err := core.NewID()
	if err != nil {
		return "", fmt.Errorf("create upload identifier: %w", err)
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, s.store.DeleteImportUpload(ctx, id))
		}
	}()
	if err := s.write(ctx, id, upload, clock); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Staging) write(ctx context.Context, id string, upload io.Reader, clock core.Clock) error {
	reader := io.LimitReader(upload, s.maxBytes+1)
	chunks := make([]core.ImportUploadChunk, 0, core.MaxImportUploadWriteChunks)
	var total int64
	maxChunks := int(s.maxBytes/int64(core.ImportUploadChunkBytes)) + 2
	for index := range maxChunks {
		data, done, err := readUploadChunk(reader)
		if err != nil {
			return err
		}
		total += int64(len(data))
		if total > s.maxBytes {
			return fmt.Errorf("upload exceeds size limit: %w", core.ErrInvalidArgument)
		}
		if len(data) > 0 || index == 0 {
			chunks = append(chunks, core.ImportUploadChunk{
				ID: id, Index: int64(index), Bytes: data,
				CreatedAt: core.NormalizeTime(clock.Now()),
			})
		}
		if len(chunks) > 0 && (len(chunks) == core.MaxImportUploadWriteChunks || done) {
			if err := s.store.WriteImportUploadChunks(ctx, chunks); err != nil {
				return fmt.Errorf("write import upload chunks: %w", err)
			}
			chunks = chunks[:0]
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("upload exceeds chunk bound: %w", core.ErrInvalidArgument)
}

func readUploadChunk(reader io.Reader) ([]byte, bool, error) {
	buffer := make([]byte, core.ImportUploadChunkBytes)
	count, err := io.ReadFull(reader, buffer)
	switch {
	case err == nil:
		return buffer, false, nil
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return buffer[:count], true, nil
	default:
		return nil, false, fmt.Errorf("read import upload: %w", err)
	}
}

func (s *Staging) open(ctx context.Context, id string) (*UploadReader, error) {
	return s.openWithTimeout(ctx, id, 0)
}

func (s *Staging) openWithTimeout(
	ctx context.Context, id string, timeout time.Duration,
) (*UploadReader, error) {
	if s == nil || !core.ValidID(id) {
		return nil, core.ErrInvalidArgument
	}
	infoCtx, cancel := storeCallContext(ctx, timeout)
	info, err := s.store.ImportUploadInfo(infoCtx, id)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("read import upload info: %w", err)
	}
	if !validUploadInfo(info) {
		return nil, fmt.Errorf("validate import upload info: %w", core.ErrInvalidArgument)
	}
	readChunk := func(index int64) ([]byte, error) {
		readCtx, cancelRead := storeCallContext(ctx, timeout)
		defer cancelRead()
		return s.store.ReadImportUploadChunk(readCtx, id, index)
	}
	return &UploadReader{info: info, readChunk: readChunk, cachedIndex: -1}, nil
}

func storeCallContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func validUploadInfo(info core.ImportUploadInfo) bool {
	if !core.ValidID(info.ID) || info.Size < 0 || info.Size > core.MaxImportUploadBytes || info.ChunkCount < 1 {
		return false
	}
	want := int64(1)
	if info.Size > 0 {
		want = (info.Size + int64(core.ImportUploadChunkBytes) - 1) / int64(core.ImportUploadChunkBytes)
	}
	return info.ChunkCount == want
}

func (s *Staging) remove(ctx context.Context, id string) error {
	if s == nil || !core.ValidID(id) {
		return core.ErrInvalidArgument
	}
	return s.store.DeleteImportUpload(ctx, id)
}

func (s *Staging) sweep(ctx context.Context, now time.Time) (int64, error) {
	if s == nil || now.IsZero() {
		return 0, core.ErrInvalidArgument
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	before := core.NormalizeTime(now.Add(-s.transferTimeout))
	return s.store.DeleteOrphanImportUploads(ctx, before, core.MaxOrphanImportUploadChunks)
}

// UploadReader exposes sequential and random access over fixed-size database chunks.
type UploadReader struct {
	info        core.ImportUploadInfo
	readChunk   func(int64) ([]byte, error)
	position    int64
	cachedIndex int64
	cached      []byte
	mu          sync.Mutex
}

// Size returns the staged upload size in bytes.
func (r *UploadReader) Size() int64 { return r.info.Size }

// Read advances through the staged upload.
func (r *UploadReader) Read(buffer []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	read, err := r.readAt(buffer, r.position)
	r.position += int64(read)
	return read, err
}

// ReadAt reads across fixed-size chunk boundaries.
func (r *UploadReader) ReadAt(buffer []byte, offset int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readAt(buffer, offset)
}

func (r *UploadReader) readAt(buffer []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, core.ErrInvalidArgument
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	if offset >= r.info.Size {
		return 0, io.EOF
	}
	read := 0
	maxChunks := core.MaxImportUploadBytes/core.ImportUploadChunkBytes + 1
	for range maxChunks {
		count, err := r.copyChunk(buffer[read:], offset+int64(read))
		read += count
		if err != nil {
			return read, err
		}
		if read == len(buffer) || offset+int64(read) == r.info.Size {
			break
		}
	}
	if read < len(buffer) {
		return read, io.EOF
	}
	return read, nil
}

func (r *UploadReader) copyChunk(destination []byte, offset int64) (int, error) {
	index := offset / int64(core.ImportUploadChunkBytes)
	chunk, err := r.chunk(index)
	if err != nil {
		return 0, err
	}
	start := offset % int64(core.ImportUploadChunkBytes)
	if start >= int64(len(chunk)) {
		return 0, fmt.Errorf("validate import upload chunk: %w", core.ErrInvalidArgument)
	}
	return copy(destination, chunk[start:]), nil
}

func (r *UploadReader) chunk(index int64) ([]byte, error) {
	if index == r.cachedIndex {
		return r.cached, nil
	}
	chunk, err := r.readChunk(index)
	if err != nil {
		return nil, fmt.Errorf("read import upload chunk: %w", err)
	}
	expected := core.ImportUploadChunkBytes
	if index == r.info.ChunkCount-1 {
		expected = int(r.info.Size - index*int64(core.ImportUploadChunkBytes))
	}
	if len(chunk) != expected {
		return nil, fmt.Errorf("validate import upload chunk: %w", core.ErrInvalidArgument)
	}
	r.cachedIndex, r.cached = index, chunk
	return chunk, nil
}
