package downloadmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/secrets"
	"github.com/BonzTM/bloom/internal/testutil"
)

type serviceStore struct {
	record  core.DownloadManagerRecord
	creates int
}

func (s *serviceStore) GetDownloadManager(context.Context, string) (core.DownloadManagerRecord, error) {
	return s.record, nil
}

func (s *serviceStore) GetDownloadManagerByName(context.Context, string) (core.DownloadManagerRecord, error) {
	return s.record, nil
}

func (s *serviceStore) ListDownloadManagers(context.Context, string, int) ([]core.DownloadManager, error) {
	return []core.DownloadManager{s.record.DownloadManager}, nil
}

func (s *serviceStore) CreateDownloadManager(_ context.Context, record core.DownloadManagerRecord) error {
	s.creates++
	s.record = record
	return nil
}

func (s *serviceStore) DeleteDownloadManager(context.Context, string) error { return nil }

type serviceCipher struct{ plaintext string }

func (c *serviceCipher) Encrypt(plaintext []byte, _ secrets.Context) ([]byte, error) {
	c.plaintext = string(plaintext)
	return []byte("encrypted-envelope"), nil
}

func (*serviceCipher) Decrypt([]byte, secrets.Context) ([]byte, error) {
	return []byte("secret-key"), nil
}
func (*serviceCipher) KeyID() string { return "key-1" }

type serviceFactory struct {
	adapter    core.DownloadManagerAdapter
	credential string
}

func (f *serviceFactory) New(_ core.DownloadManagerKind, _, credential string, _ bool) (core.DownloadManagerAdapter, error) {
	f.credential = credential
	return f.adapter, nil
}

type serviceAdapter struct{ probeErr error }

func (a *serviceAdapter) Probe(context.Context) (core.DownloadManagerInfo, error) {
	if a.probeErr != nil {
		return core.DownloadManagerInfo{}, a.probeErr
	}
	return core.DownloadManagerInfo{
		Name: "Radarr", Version: "5.0", Capabilities: core.DownloadManagerCapabilities{Kinds: []core.MediaKind{core.MediaKindMovie}},
	}, nil
}

func (*serviceAdapter) Add(context.Context, core.DownloadTitle, core.DownloadOptions) (string, error) {
	return "1", nil
}

func (*serviceAdapter) Queue(context.Context, string, []int) (core.DownloadProgress, error) {
	return core.DownloadProgress{}, nil
}

func TestRegisterProbesBeforeEncryptedPersistence(t *testing.T) {
	const apiKey = "download-manager-super-secret"
	store, cipher := &serviceStore{}, &serviceCipher{}
	factory := &serviceFactory{adapter: &serviceAdapter{}}
	service, err := NewService(store, store, cipher, factory, testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	connection, err := service.Register(t.Context(), core.DownloadManagerKindRadarr, "Main", "https://radarr.example", apiKey, false)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if store.creates != 1 || cipher.plaintext != apiKey || factory.credential != apiKey {
		t.Fatalf("registration calls = creates %d plaintext %q credential %q", store.creates, cipher.plaintext, factory.credential)
	}
	if string(store.record.CredentialCiphertext) != "encrypted-envelope" || strings.Contains(fmt.Sprintf("%+v", connection), apiKey) {
		t.Fatalf("registration leaked or failed to encrypt the credential: record=%+v response=%+v", store.record, connection)
	}
}

func TestRegisterDoesNotPersistFailedProbe(t *testing.T) {
	store := &serviceStore{}
	probeErr := &core.DownloadManagerError{Kind: core.DownloadManagerUnauthorized, Operation: "probe"}
	service, err := NewService(
		store, store, &serviceCipher{}, &serviceFactory{adapter: &serviceAdapter{probeErr: probeErr}},
		testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_, err = service.Register(t.Context(), core.DownloadManagerKindRadarr, "Main", "https://radarr.example", "secret", false)
	if !errors.Is(err, probeErr) || store.creates != 0 {
		t.Fatalf("Register = %v; creates %d", err, store.creates)
	}
}
