package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

const defaultAccountPageSize = 50

type accountCursorPayload struct {
	Query       string `json:"q"`
	UsernameKey string `json:"username_key"`
	ID          string `json:"id"`
}

func accountListParams(r *http.Request) (core.AccountListQuery, []httputil.FieldError) {
	values := r.URL.Query()
	limit, limitMessage := parseAccountLimit(values["limit"])
	searchKey, searchMessage := parseAccountSearch(values["q"])
	after, cursorMessage := parseAccountCursor(values["cursor"], searchKey)
	fields := make([]httputil.FieldError, 0, 3)
	fields = appendAccountField(fields, "limit", limitMessage)
	fields = appendAccountField(fields, "q", searchMessage)
	fields = appendAccountField(fields, "cursor", cursorMessage)
	return core.AccountListQuery{SearchKey: searchKey, After: after, Limit: limit}, fields
}

func appendAccountField(fields []httputil.FieldError, field, message string) []httputil.FieldError {
	if message == "" {
		return fields
	}
	return append(fields, httputil.FieldError{Field: field, Code: "invalid", Message: message})
}

func parseAccountLimit(values []string) (int, string) {
	if len(values) == 0 {
		return defaultAccountPageSize, ""
	}
	if len(values) != 1 || values[0] == "" {
		return defaultAccountPageSize, "must appear once"
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < 1 || value > core.MaxAccountListPageSize {
		return defaultAccountPageSize, "must be an integer from 1 through 100"
	}
	return value, ""
}

func parseAccountSearch(values []string) (string, string) {
	if len(values) == 0 {
		return "", ""
	}
	if len(values) != 1 {
		return "", "must appear once"
	}
	key, err := core.AccountSearchKey(values[0])
	if err != nil {
		return "", "must contain at most 128 valid UTF-8 bytes"
	}
	return key, ""
}

func parseAccountCursor(values []string, searchKey string) (*core.AccountListPosition, string) {
	if len(values) == 0 {
		return nil, ""
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > core.MaxAccountListCursorBytes {
		return nil, "must appear once and be bounded"
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
	if err != nil || len(data) > core.MaxAccountListCursorBytes {
		return nil, "is invalid"
	}
	var payload accountCursorPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&payload); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, "is invalid"
	}
	position := &core.AccountListPosition{UsernameKey: payload.UsernameKey, ID: payload.ID}
	probe := core.AccountListQuery{SearchKey: payload.Query, After: position, Limit: 1}
	if payload.Query != searchKey || !probe.Valid() {
		return nil, "does not match this query"
	}
	return position, ""
}

func encodeAccountCursor(query string, account core.AdminAccount) (string, error) {
	payload := accountCursorPayload{Query: query, UsernameKey: account.UsernameKey, ID: account.ID}
	position := &core.AccountListPosition{UsernameKey: payload.UsernameKey, ID: payload.ID}
	if !(&core.AccountListQuery{SearchKey: payload.Query, After: position, Limit: 1}).Valid() {
		return "", fmt.Errorf("encode account cursor: %w", core.ErrInvalidArgument)
	}
	data, err := json.Marshal(payload)
	if err != nil || len(data) > core.MaxAccountListCursorBytes {
		return "", fmt.Errorf("encode account cursor: %w", core.ErrInvalidArgument)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	if len(encoded) > core.MaxAccountListCursorBytes {
		return "", fmt.Errorf("encode account cursor: %w", core.ErrInvalidArgument)
	}
	return encoded, nil
}
