package importer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

type memoryUploadStore struct {
	mu     sync.Mutex
	chunks map[string]map[int64][]byte
	newest map[string]time.Time
	linked map[string]bool
}

func newMemoryUploadStore() *memoryUploadStore {
	return &memoryUploadStore{
		chunks: make(map[string]map[int64][]byte),
		newest: make(map[string]time.Time),
		linked: make(map[string]bool),
	}
}

func (s *memoryUploadStore) WriteImportUploadChunks(_ context.Context, chunks []core.ImportUploadChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chunk := range chunks {
		if s.chunks[chunk.ID] == nil {
			s.chunks[chunk.ID] = make(map[int64][]byte)
		}
		s.chunks[chunk.ID][chunk.Index] = bytes.Clone(chunk.Bytes)
		if chunk.CreatedAt.After(s.newest[chunk.ID]) {
			s.newest[chunk.ID] = chunk.CreatedAt
		}
	}
	return nil
}

func (s *memoryUploadStore) ImportUploadInfo(_ context.Context, id string) (core.ImportUploadInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chunks, ok := s.chunks[id]
	if !ok {
		return core.ImportUploadInfo{}, core.ErrNotFound
	}
	var size int64
	for _, chunk := range chunks {
		size += int64(len(chunk))
	}
	return core.ImportUploadInfo{ID: id, Size: size, ChunkCount: int64(len(chunks))}, nil
}

func (s *memoryUploadStore) ReadImportUploadChunk(_ context.Context, id string, index int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chunk, ok := s.chunks[id][index]
	if !ok {
		return nil, core.ErrNotFound
	}
	return bytes.Clone(chunk), nil
}

func (s *memoryUploadStore) DeleteImportUpload(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.linked[id] {
		return nil
	}
	s.deleteUpload(id)
	return nil
}

func (s *memoryUploadStore) deleteUpload(id string) {
	delete(s.chunks, id)
	delete(s.newest, id)
	delete(s.linked, id)
}

func (s *memoryUploadStore) linkUpload(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.linked[id] = true
}

func (s *memoryUploadStore) DeleteOrphanImportUploads(
	_ context.Context, before time.Time, limit int,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var deleted int64
	for id, newest := range s.newest {
		if deleted == int64(limit) {
			break
		}
		if s.linked[id] || !newest.Before(before) {
			continue
		}
		for index := range s.chunks[id] {
			delete(s.chunks[id], index)
			deleted++
			if deleted == int64(limit) {
				break
			}
		}
		if len(s.chunks[id]) == 0 {
			s.deleteUpload(id)
		}
	}
	return deleted, nil
}

func TestDatabaseStagingStreamsThreeMiBAcrossChunkBoundaries(t *testing.T) {
	store := newMemoryUploadStore()
	staging, err := NewStaging(store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	payload := make([]byte, 3*core.ImportUploadChunkBytes+17)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	id, err := staging.stage(t.Context(), bytes.NewReader(payload), staticClock{now})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	reader, err := staging.open(t.Context(), id)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	window := make([]byte, 64)
	offset := int64(core.ImportUploadChunkBytes - 23)
	if _, readErr := reader.ReadAt(window, offset); readErr != nil {
		t.Fatalf("ReadAt across boundary: %v", readErr)
	}
	if !bytes.Equal(window, payload[offset:offset+int64(len(window))]) {
		t.Fatal("ReadAt returned different bytes across a chunk boundary")
	}
	all, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(all, payload) {
		t.Fatalf("Read = %d bytes, %v", len(all), err)
	}
	info, err := store.ImportUploadInfo(t.Context(), id)
	if err != nil || info.ChunkCount != 4 || info.Size != int64(len(payload)) {
		t.Fatalf("upload info = %+v, %v", info, err)
	}
}

func TestDatabaseStagingEnforcesUploadCapWithoutResidue(t *testing.T) {
	store := newMemoryUploadStore()
	staging, err := NewStaging(store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	staging.maxBytes = 8
	if _, err := staging.stage(
		t.Context(), bytes.NewReader([]byte("123456789")), staticClock{time.Now()},
	); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("oversize stage = %v, want invalid argument", err)
	}
	if len(store.chunks) != 0 {
		t.Fatalf("oversize upload left %d staged uploads", len(store.chunks))
	}
}

func TestDatabaseStagingDeletesOnlyExpiredOrphans(t *testing.T) {
	store := newMemoryUploadStore()
	staging, err := NewStaging(store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	oldID, err := staging.stage(t.Context(), bytes.NewReader([]byte("old")), staticClock{now.Add(-11 * time.Minute)})
	if err != nil {
		t.Fatalf("stage old: %v", err)
	}
	newID, err := staging.stage(t.Context(), bytes.NewReader([]byte("new")), staticClock{now})
	if err != nil {
		t.Fatalf("stage new: %v", err)
	}
	deleted, err := staging.sweep(t.Context(), now)
	if err != nil || deleted != 1 {
		t.Fatalf("sweep = %d, %v", deleted, err)
	}
	if _, err := store.ImportUploadInfo(t.Context(), oldID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("old orphan remains: %v", err)
	}
	if _, err := store.ImportUploadInfo(t.Context(), newID); err != nil {
		t.Fatalf("recent orphan removed: %v", err)
	}
}

func TestSecondStagingDoesNotSweepUploadWithRecentChunks(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	clock := &controlledClock{now: now.Add(-11 * time.Minute)}
	recentWrite := make(chan struct{})
	store := &observedUploadStore{memoryUploadStore: newMemoryUploadStore()}
	store.afterWrite = func(call int) {
		if call == 1 {
			clock.Set(now)
		}
		if call == 2 {
			close(recentWrite)
		}
	}
	first := mustStaging(t, store)
	second := mustStaging(t, store)
	reader, writer := io.Pipe()
	staged := make(chan stageResult, 1)
	go func() {
		id, err := first.stage(t.Context(), reader, clock)
		staged <- stageResult{id: id, err: err}
	}()
	writeDone := writeUploadChunks(writer, 2*core.MaxImportUploadWriteChunks)
	<-recentWrite
	deleted, err := second.sweep(t.Context(), now)
	if err != nil || deleted != 0 {
		t.Fatalf("concurrent sweep = %d, %v", deleted, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close upload writer: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write upload: %v", err)
	}
	result := <-staged
	if result.err != nil {
		t.Fatalf("stage upload: %v", result.err)
	}
	if _, err := store.ImportUploadInfo(t.Context(), result.id); err != nil {
		t.Fatalf("active upload was swept: %v", err)
	}
}

type controlledClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *controlledClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *controlledClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

type observedUploadStore struct {
	*memoryUploadStore
	afterWrite func(int)
	writes     int
}

func (s *observedUploadStore) WriteImportUploadChunks(
	ctx context.Context, chunks []core.ImportUploadChunk,
) error {
	if err := s.memoryUploadStore.WriteImportUploadChunks(ctx, chunks); err != nil {
		return err
	}
	s.writes++
	s.afterWrite(s.writes)
	return nil
}

type stageResult struct {
	id  string
	err error
}

func mustStaging(t *testing.T, store uploadStore) *Staging {
	t.Helper()
	staging, err := NewStaging(store, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	return staging
}

func writeUploadChunks(writer io.Writer, count int) <-chan error {
	done := make(chan error, 1)
	go func() {
		chunk := make([]byte, core.ImportUploadChunkBytes)
		for range count {
			if _, err := writer.Write(chunk); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	return done
}

type staticClock struct{ now time.Time }

func (c staticClock) Now() time.Time { return c.now }
