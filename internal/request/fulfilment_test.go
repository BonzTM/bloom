package request

import (
	"context"
	"errors"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

type managerResolverFunc func(context.Context, string) (core.DownloadManager, error)

func (f managerResolverFunc) Resolve(ctx context.Context, name string) (core.DownloadManager, error) {
	return f(ctx, name)
}

func TestNormalizeProfileTargetCanonicalizesRegisteredManager(t *testing.T) {
	service := &Service{managers: managerResolverFunc(func(_ context.Context, name string) (core.DownloadManager, error) {
		if name != "MAIN RADARR" {
			t.Fatalf("manager lookup name = %q", name)
		}
		return core.DownloadManager{Kind: core.DownloadManagerKindRadarr, Name: "Main Radarr"}, nil
	})}
	profile := core.RequestProfile{
		Kinds: []core.MediaKind{core.MediaKindMovie}, DownloadManagerKind: "radarr", DownloadManagerInstance: "MAIN RADARR",
	}
	if err := service.normalizeProfileTarget(t.Context(), &profile); err != nil {
		t.Fatalf("normalizeProfileTarget: %v", err)
	}
	if profile.DownloadManagerInstance != "Main Radarr" || profile.DownloadManagerKind != "radarr" {
		t.Fatalf("normalized profile = %+v", profile)
	}
}

func TestNormalizeProfileTargetRejectsMismatchAndUnsupportedKind(t *testing.T) {
	service := &Service{managers: managerResolverFunc(func(context.Context, string) (core.DownloadManager, error) {
		return core.DownloadManager{Kind: core.DownloadManagerKindSonarr, Name: "Sonarr"}, nil
	})}
	tests := []core.RequestProfile{
		{Kinds: []core.MediaKind{core.MediaKindSeries}, DownloadManagerKind: "radarr", DownloadManagerInstance: "Sonarr"},
		{Kinds: []core.MediaKind{core.MediaKindMovie}, DownloadManagerKind: "sonarr", DownloadManagerInstance: "Sonarr"},
	}
	for _, profile := range tests {
		if err := service.normalizeProfileTarget(t.Context(), &profile); !errors.Is(err, core.ErrInvalidArgument) {
			t.Fatalf("normalizeProfileTarget(%+v) = %v", profile, err)
		}
	}
}
