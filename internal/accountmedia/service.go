// Package accountmedia manages Bloom-account links to media-server users.
package accountmedia

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/BonzTM/bloom/internal/core"
)

// MaxMatchServersPerRequest bounds on-demand external lookups.
const MaxMatchServersPerRequest = 8

type serverAccess interface {
	List(ctx context.Context, afterNameKey string, pageSize int) ([]core.MediaServerConnection, error)
	Get(ctx context.Context, id string) (core.MediaServerConnection, error)
	FindUserByName(ctx context.Context, id, name string) (core.MediaUser, bool, bool, error)
	FindUserByID(ctx context.Context, id, mediaUserID string) (core.MediaUser, bool, bool, error)
}

// MatchMetrics records finite on-demand match outcomes.
type MatchMetrics interface {
	IncMediaUserMatch(outcome string)
}

// Dependencies are the required account-media-link boundaries.
type Dependencies struct {
	Reader   core.AccountMediaUserReader
	Writer   core.AccountMediaUserWriter
	Accounts core.AccountStore
	Servers  serverAccess
	Clock    core.Clock
	Logger   *slog.Logger
	Metrics  MatchMetrics
}

// Service coordinates link administration and on-demand username matching.
type Service struct{ deps Dependencies }

// NewService validates dependencies.
func NewService(deps Dependencies) (*Service, error) {
	if deps.Reader == nil || deps.Writer == nil || deps.Accounts == nil || deps.Servers == nil ||
		deps.Clock == nil || deps.Logger == nil || deps.Metrics == nil {
		return nil, fmt.Errorf("account media service: %w", core.ErrInvalidArgument)
	}
	return &Service{deps: deps}, nil
}

// EnsureLinks returns current links after bounded exact-username matching.
func (s *Service) EnsureLinks(
	ctx context.Context, account core.Account, mediaServerID string,
) ([]core.AccountMediaUser, error) {
	if !core.ValidID(account.ID) || account.Username == "" {
		return nil, core.ErrInvalidArgument
	}
	if mediaServerID != "" {
		return s.ensureServerLink(ctx, account, mediaServerID)
	}
	servers, err := s.deps.Servers.List(ctx, "", MaxMatchServersPerRequest)
	if err != nil {
		return nil, fmt.Errorf("list media servers for account match: %w", err)
	}
	for index, server := range servers {
		if index >= MaxMatchServersPerRequest {
			break
		}
		if err := s.tryMatch(ctx, account, server.Server.ID); err != nil {
			return nil, err
		}
	}
	return s.deps.Reader.ListAccountMediaUsers(ctx, account.ID, false, core.MaxAccountMediaUsers)
}

func (s *Service) ensureServerLink(
	ctx context.Context, account core.Account, serverID string,
) ([]core.AccountMediaUser, error) {
	if !core.ValidID(serverID) {
		return nil, core.ErrInvalidArgument
	}
	if _, err := s.deps.Servers.Get(ctx, serverID); err != nil {
		return nil, fmt.Errorf("get media server for account match: %w", err)
	}
	if err := s.tryMatch(ctx, account, serverID); err != nil {
		return nil, err
	}
	link, err := s.deps.Reader.GetAccountMediaUser(ctx, account.ID, serverID)
	if errors.Is(err, core.ErrNotFound) {
		return []core.AccountMediaUser{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get account media user: %w", err)
	}
	return []core.AccountMediaUser{link}, nil
}

func (s *Service) tryMatch(ctx context.Context, account core.Account, serverID string) error {
	if _, err := s.deps.Reader.GetAccountMediaUser(ctx, account.ID, serverID); err == nil {
		return nil
	} else if !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("check account media user: %w", err)
	}
	user, found, supported, err := s.deps.Servers.FindUserByName(ctx, serverID, account.Username)
	if err != nil {
		s.observeLookupFailure(ctx, serverID, err)
		return nil
	}
	if !supported || !found {
		s.deps.Metrics.IncMediaUserMatch(matchOutcome(supported, found))
		return nil
	}
	return s.persistMatch(ctx, account.ID, serverID, user)
}

func (s *Service) observeLookupFailure(ctx context.Context, serverID string, err error) {
	s.deps.Metrics.IncMediaUserMatch("error")
	s.deps.Logger.DebugContext(ctx, "media user username lookup failed", "media_server_id", serverID, "error", err)
}

func matchOutcome(supported, found bool) string {
	if !supported {
		return "unsupported"
	}
	if !found {
		return "not_found"
	}
	return "found"
}

func (s *Service) persistMatch(ctx context.Context, accountID, serverID string, user core.MediaUser) error {
	now := core.NormalizeTime(s.deps.Clock.Now())
	link := core.AccountMediaUser{
		AccountID: accountID, MediaServerID: serverID, MediaUserID: user.ID,
		Username: user.Name, Source: core.AccountMediaUserSourceMatch, CreatedAt: now, UpdatedAt: now,
	}
	created, err := s.deps.Writer.CreateAccountMediaUserIfAbsent(ctx, link)
	if err != nil {
		return fmt.Errorf("persist matched account media user: %w", err)
	}
	outcome := "conflict"
	if created {
		outcome = "found"
	}
	s.deps.Metrics.IncMediaUserMatch(outcome)
	return nil
}

// List returns one account's links after verifying the account exists.
func (s *Service) List(ctx context.Context, accountID string, includeSuppressed bool) ([]core.AccountMediaUser, error) {
	if _, err := s.deps.Accounts.GetAccount(ctx, accountID); err != nil {
		return nil, fmt.Errorf("get linked account: %w", err)
	}
	links, err := s.deps.Reader.ListAccountMediaUsers(ctx, accountID, includeSuppressed, core.MaxAccountMediaUsers)
	if err != nil {
		return nil, fmt.Errorf("list account media users: %w", err)
	}
	return links, nil
}

// Set verifies and stores one administrator-managed link.
func (s *Service) Set(
	ctx context.Context, accountID, serverID, mediaUserID string,
) (core.AccountMediaUser, error) {
	if !core.ValidID(accountID) || !core.ValidID(serverID) || !core.ValidAccountMediaUserID(mediaUserID) {
		return core.AccountMediaUser{}, core.ErrInvalidArgument
	}
	account, err := s.deps.Accounts.GetAccount(ctx, accountID)
	if err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("get linked account: %w", err)
	}
	if _, getErr := s.deps.Servers.Get(ctx, serverID); getErr != nil {
		return core.AccountMediaUser{}, fmt.Errorf("get linked media server: %w", getErr)
	}
	username, err := s.verifiedUsername(ctx, serverID, mediaUserID, account.Username)
	if err != nil {
		return core.AccountMediaUser{}, err
	}
	now := core.NormalizeTime(s.deps.Clock.Now())
	link := core.AccountMediaUser{
		AccountID: accountID, MediaServerID: serverID, MediaUserID: mediaUserID, Username: username,
		Source: core.AccountMediaUserSourceAdmin, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.deps.Writer.SetAccountMediaUser(ctx, link); err != nil {
		return core.AccountMediaUser{}, fmt.Errorf("set account media user: %w", err)
	}
	return s.deps.Reader.GetAccountMediaUser(ctx, accountID, serverID)
}

func (s *Service) verifiedUsername(
	ctx context.Context, serverID, mediaUserID, fallback string,
) (string, error) {
	user, found, supported, err := s.deps.Servers.FindUserByID(ctx, serverID, mediaUserID)
	if err != nil {
		return "", fmt.Errorf("verify media user: %w", err)
	}
	if supported && !found {
		return "", core.ErrNotFound
	}
	if supported {
		return user.Name, nil
	}
	return fallback, nil
}

// Delete suppresses one link so automatic matching cannot recreate it.
func (s *Service) Delete(ctx context.Context, accountID, serverID string) error {
	if _, err := s.deps.Accounts.GetAccount(ctx, accountID); err != nil {
		return fmt.Errorf("get linked account: %w", err)
	}
	now := core.NormalizeTime(s.deps.Clock.Now())
	if err := s.deps.Writer.SuppressAccountMediaUser(ctx, accountID, serverID, now); err != nil {
		return fmt.Errorf("suppress account media user: %w", err)
	}
	return nil
}
