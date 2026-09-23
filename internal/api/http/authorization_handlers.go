package http

import (
	"context"
	"encoding/base64"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

const (
	defaultRolePageSize = 50
	maxRolePageSize     = 100
	maxRoleCursorBytes  = (core.MaxRoleNameBytes*8 + 5) / 6
)

type permissionResponse struct {
	ID     core.Permission `json:"id"`
	Module string          `json:"module"`
}

type permissionsResponse struct {
	Permissions []permissionResponse `json:"permissions"`
}

type currentAccountResponse struct {
	Account     accountResponse   `json:"account"`
	Roles       []string          `json:"roles"`
	Permissions []core.Permission `json:"permissions"`
}

type roleResponse struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	BuiltIn     bool              `json:"built_in"`
	CreatedAt   time.Time         `json:"created_at"`
	Permissions []core.Permission `json:"permissions"`
}

type rolesResponse struct {
	Items      []roleResponse `json:"items"`
	NextCursor string         `json:"next_cursor"`
}

func (s *Server) handlePermissions(w http.ResponseWriter, r *http.Request) {
	catalog := core.PermissionCatalog()
	permissions := make([]permissionResponse, 0, len(catalog))
	for _, definition := range catalog {
		permissions = append(permissions, permissionResponse{ID: definition.ID, Module: definition.Module})
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, r, s.logger, http.StatusOK, permissionsResponse{Permissions: permissions})
}

func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	afterName, pageSize, fields := rolePageParams(r)
	if len(fields) != 0 {
		s.writeValidation(w, r, fields)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.authOperationTimeout)
	defer cancel()
	roles, err := s.roles.ListRoles(ctx, afterName, pageSize+1)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	roles, nextCursor := rolePage(roles, pageSize)
	response := make([]roleResponse, 0, len(roles))
	for _, role := range roles {
		permissions := slices.Clone(role.Permissions)
		slices.Sort(permissions)
		response = append(response, roleResponse{
			ID: role.ID, Name: role.Name, Description: role.Description,
			BuiltIn: role.BuiltIn, CreatedAt: role.CreatedAt, Permissions: permissions,
		})
	}
	writeJSON(w, r, s.logger, http.StatusOK, rolesResponse{Items: response, NextCursor: nextCursor})
}

func rolePageParams(r *http.Request) (string, int, []httputil.FieldError) {
	query := r.URL.Query()
	pageSize, sizeErr := parseRolePageSize(query["page_size"])
	afterName, cursorErr := parseRoleCursor(query["cursor"])
	fields := make([]httputil.FieldError, 0, 2)
	if sizeErr != "" {
		fields = append(fields, httputil.FieldError{Field: "page_size", Code: "invalid", Message: sizeErr})
	}
	if cursorErr != "" {
		fields = append(fields, httputil.FieldError{Field: "cursor", Code: "invalid", Message: cursorErr})
	}
	return afterName, pageSize, fields
}

func parseRolePageSize(values []string) (int, string) {
	if len(values) == 0 {
		return defaultRolePageSize, ""
	}
	if len(values) != 1 || values[0] == "" {
		return defaultRolePageSize, "must appear once"
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < 1 {
		return defaultRolePageSize, "must be a positive integer"
	}
	return min(value, maxRolePageSize), ""
}

func parseRoleCursor(values []string) (string, string) {
	if len(values) == 0 {
		return "", ""
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > maxRoleCursorBytes {
		return "", "must be one valid cursor"
	}
	decoded, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil || !core.ValidRoleName(string(decoded)) {
		return "", "must be one valid cursor"
	}
	return string(decoded), ""
}

func rolePage(roles []core.Role, pageSize int) ([]core.Role, string) {
	if len(roles) <= pageSize {
		return roles, ""
	}
	page := roles[:pageSize]
	next := base64.RawURLEncoding.EncodeToString([]byte(page[len(page)-1].Name))
	return page, next
}
