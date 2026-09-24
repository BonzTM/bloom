package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/db/postgres"
	"github.com/BonzTM/bloom/internal/db/sqlite"
)

func requestCreationEvents(request core.MediaRequest, events []core.RequestEvent) ([]core.RequestEvent, error) {
	if err := core.ValidateMediaRequest(request); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		events = []core.RequestEvent{{
			Type: core.RequestEventCreated, RequestID: request.ID, RequesterID: request.RequesterID,
			ActorID: request.RequesterID, Kind: request.Kind, Title: request.Title,
			Status: request.Status, At: request.UpdatedAt,
		}}
	}
	if len(events) < 1 || len(events) > 2 {
		return nil, core.ErrInvalidArgument
	}
	for _, event := range events {
		if event.RequestID != request.ID || event.RequesterID != request.RequesterID ||
			event.Kind != request.Kind || event.Title != request.Title || event.Status != request.Status ||
			core.ValidateRequestEvent(event) != nil {
			return nil, core.ErrInvalidArgument
		}
	}
	return events, nil
}

func validateTransitionEvent(event core.RequestEvent, requestID string, status core.RequestStatus, at time.Time) error {
	if event.RequestID != requestID || event.Status != status || !event.At.Equal(core.NormalizeTime(at)) {
		return core.ErrInvalidArgument
	}
	return core.ValidateRequestEvent(event)
}

func (s *requests) requestTransitionEvent(
	ctx context.Context, tx *sql.Tx, id string, status core.RequestStatus, actorID, reason string, at time.Time,
	events []core.RequestEvent,
) (core.RequestEvent, error) {
	request, err := s.requestInTransaction(ctx, tx, id)
	if err != nil {
		return core.RequestEvent{}, err
	}
	expectedActor := actorID
	if expectedActor == "" {
		expectedActor = "system"
	}
	if len(events) == 1 {
		event := events[0]
		if err := validateTransitionEvent(event, id, status, at); err != nil {
			return core.RequestEvent{}, err
		}
		if event.RequesterID != request.RequesterID || event.ActorID != expectedActor ||
			event.Kind != request.Kind || event.Title != request.Title || event.Reason != reason {
			return core.RequestEvent{}, core.ErrInvalidArgument
		}
		return event, nil
	}
	event := core.RequestEvent{
		Type: requestEventType(status), RequestID: id, RequesterID: request.RequesterID,
		ActorID: expectedActor, Kind: request.Kind, Title: request.Title, Status: status,
		Reason: reason, At: core.NormalizeTime(at),
	}
	return event, core.ValidateRequestEvent(event)
}

func (s *requests) requestInTransaction(ctx context.Context, tx *sql.Tx, id string) (core.MediaRequest, error) {
	if s.sqlite != nil {
		row, err := sqlite.New(tx).GetRequest(ctx, id)
		if err != nil {
			return core.MediaRequest{}, mapNotFound("get request for notification event", err)
		}
		return sqliteRequest(row)
	}
	row, err := postgres.New(tx).GetRequest(ctx, id)
	if err != nil {
		return core.MediaRequest{}, mapNotFound("get request for notification event", err)
	}
	return postgresRequest(row)
}

func requestEventType(status core.RequestStatus) core.RequestEventType {
	switch status {
	case core.RequestApproved:
		return core.RequestEventApproved
	case core.RequestDeclined:
		return core.RequestEventDeclined
	case core.RequestProcessing:
		return core.RequestEventDispatched
	case core.RequestAvailable:
		return core.RequestEventAvailable
	default:
		return core.RequestEventFailed
	}
}

func (s *requests) insertNotificationEvent(ctx context.Context, tx *sql.Tx, event core.RequestEvent) error {
	if s.sqlite != nil {
		return insertSQLiteNotificationEvents(ctx, sqlite.New(tx), []core.RequestEvent{event})
	}
	return insertPostgresNotificationEvents(ctx, postgres.New(tx), []core.RequestEvent{event})
}

func insertSQLiteNotificationEvents(ctx context.Context, q *sqlite.Queries, events []core.RequestEvent) error {
	for sequence, event := range events {
		id, err := core.NewID()
		if err != nil {
			return fmt.Errorf("create notification event id: %w", err)
		}
		stamp := formatSQLiteTime(event.At)
		err = q.CreateNotificationEvent(ctx, sqlite.CreateNotificationEventParams{
			ID: id, EventType: string(event.Type), RequestID: event.RequestID, RequesterID: event.RequesterID,
			ActorID: event.ActorID, MediaKind: string(event.Kind), Title: event.Title,
			RequestStatus: string(event.Status), Reason: event.Reason, EventSequence: int64(sequence),
			OccurredAt: stamp, CreatedAt: stamp,
		})
		if err != nil {
			return fmt.Errorf("insert notification event: %w", err)
		}
	}
	return nil
}

func insertPostgresNotificationEvents(ctx context.Context, q *postgres.Queries, events []core.RequestEvent) error {
	for sequence, event := range events {
		id, err := core.NewID()
		if err != nil {
			return fmt.Errorf("create notification event id: %w", err)
		}
		stamp := core.NormalizeTime(event.At)
		err = q.CreateNotificationEvent(ctx, postgres.CreateNotificationEventParams{
			ID: id, EventType: string(event.Type), RequestID: event.RequestID, RequesterID: event.RequesterID,
			ActorID: event.ActorID, MediaKind: string(event.Kind), Title: event.Title,
			RequestStatus: string(event.Status), Reason: event.Reason, EventSequence: int32(sequence),
			OccurredAt: stamp, CreatedAt: stamp,
		})
		if err != nil {
			return fmt.Errorf("insert notification event: %w", err)
		}
	}
	return nil
}
