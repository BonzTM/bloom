package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

// MetadataRequestStates resolves the caller's latest request for one provider page.
func (s *requests) MetadataRequestStates(
	ctx context.Context, accountID string, titles []core.MetadataTitle,
) (map[core.MetadataTitleKey]core.RequestStatus, error) {
	encoded, err := metadataTitleKeysJSON(accountID, titles)
	if err != nil {
		return nil, err
	}
	if len(titles) == 0 {
		return map[core.MetadataTitleKey]core.RequestStatus{}, nil
	}
	if s.sqlite != nil {
		return s.sqliteMetadataRequestStates(ctx, accountID, string(encoded))
	}
	return s.postgresMetadataRequestStates(ctx, accountID, encoded)
}

func metadataTitleKeysJSON(accountID string, titles []core.MetadataTitle) ([]byte, error) {
	if !core.ValidID(accountID) || len(titles) > core.MetadataPageSize {
		return nil, core.ErrInvalidArgument
	}
	keys := make([]string, 0, len(titles))
	for _, title := range titles {
		if core.ValidateMetadataTitle(title) != nil || title.Provider != core.MetadataProviderTMDB {
			return nil, core.ErrInvalidArgument
		}
		keys = append(keys, string(title.Kind)+":"+title.ProviderID)
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		return nil, fmt.Errorf("encode metadata title keys: %w", err)
	}
	return encoded, nil
}

func (s *requests) sqliteMetadataRequestStates(
	ctx context.Context, accountID, encoded string,
) (map[core.MetadataTitleKey]core.RequestStatus, error) {
	rows, err := s.sqlite.MetadataRequestStates(ctx, sqlite.MetadataRequestStatesParams{
		RequesterAccountID: accountID, TitleKeysJson: encoded,
	})
	if err != nil {
		return nil, fmt.Errorf("list metadata request states: %w", err)
	}
	states := make(map[core.MetadataTitleKey]core.RequestStatus, len(rows))
	for _, row := range rows {
		key := core.MetadataTitleKey{Kind: core.MediaKind(row.Kind), Provider: core.MetadataProviderKind(row.Provider), ProviderID: row.ProviderID}
		status := core.RequestStatus(row.Status)
		if !key.Kind.Valid() || !key.Provider.Valid() || core.ValidateProviderID(key.ProviderID) != nil || !status.Valid() {
			return nil, errors.New("list metadata request states: malformed stored request")
		}
		states[key] = status
	}
	return states, nil
}

func (s *requests) postgresMetadataRequestStates(
	ctx context.Context, accountID string, encoded json.RawMessage,
) (map[core.MetadataTitleKey]core.RequestStatus, error) {
	rows, err := s.postgres.MetadataRequestStates(ctx, postgres.MetadataRequestStatesParams{
		RequesterAccountID: accountID, TitleKeysJson: encoded,
	})
	if err != nil {
		return nil, fmt.Errorf("list metadata request states: %w", err)
	}
	states := make(map[core.MetadataTitleKey]core.RequestStatus, len(rows))
	for _, row := range rows {
		key := core.MetadataTitleKey{Kind: core.MediaKind(row.Kind), Provider: core.MetadataProviderKind(row.Provider), ProviderID: row.ProviderID}
		status := core.RequestStatus(row.Status)
		if !key.Kind.Valid() || !key.Provider.Valid() || core.ValidateProviderID(key.ProviderID) != nil || !status.Valid() {
			return nil, errors.New("list metadata request states: malformed stored request")
		}
		states[key] = status
	}
	return states, nil
}
