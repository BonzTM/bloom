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
	mu      sync.Mutex
	chunks  map[string]map[int64][]byte
	created map[string]time.Time
}

func newMemoryUploadStore() *memoryUploadStore {
	return &memoryUploadStore{
		chunks:  make(map[string]map[int64][]byte),
		created: make(map[string]time.Time),
	}
}

func (s *memoryUploadStore) WriteImportUploadChunks(_ context.Context, chunks []core.ImportUploadChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chunk := range chunks {
		if s.chunks[chunk.ID] == nil {
			s.chunks[chunk.ID] = make(map[int64][]byte)
			s.created[chunk.ID] = chunk.CreatedAt
		}
		s.chunks[chunk.ID][chunk.Index] = bytes.Clone(chunk.Bytes)
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
	delete(s.chunks, id)
	delete(s.created, id)
	return nil
}

func (s *memoryUploadStore) DeleteOrphanImportUploads(
	_ context.Context, before time.Time, limit int,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var deleted int64
	for id, created := range s.created {
		if deleted == int64(limit) {
			break
		}
		if created.Before(before) {
			delete(s.chunks, id)
			delete(s.created, id)
			deleted++
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
	id, err := staging.stage(t.Context(), bytes.NewReader(payload), now)
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
	if _, err := staging.stage(t.Context(), bytes.NewReader([]byte("123456789")), time.Now()); !errors.Is(err, core.ErrInvalidArgument) {
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
	oldID, err := staging.stage(t.Context(), bytes.NewReader([]byte("old")), now.Add(-11*time.Minute))
	if err != nil {
		t.Fatalf("stage old: %v", err)
	}
	newID, err := staging.stage(t.Context(), bytes.NewReader([]byte("new")), now)
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
