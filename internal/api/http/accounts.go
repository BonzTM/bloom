package http

import (
	"context"
	"net/http"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
)

type accountRoleAssignmentResponse struct {
	Name   string                 `json:"name"`
	Source core.AccountRoleSource `json:"source"`
}

type accountLinkedMediaUserResponse struct {
	MediaServerID   string `json:"media_server_id"`
	MediaServerName string `json:"media_server_name"`
	MediaUserID     string `json:"media_user_id"`
	Username        string `json:"username"`
	Suppressed      bool   `json:"suppressed"`
}

type adminAccountResponse struct {
	ID           string                           `json:"id"`
	Username     string                           `json:"username"`
	CreatedAt    time.Time                        `json:"created_at"`
	SignInMethod core.AccountSignInMethod         `json:"sign_in_method"`
	Roles        []accountRoleAssignmentResponse  `json:"roles"`
	MediaUsers   []accountLinkedMediaUserResponse `json:"media_users"`
	IsSelf       bool                             `json:"is_self"`
}

type accountsResponse struct {
	Items      []adminAccountResponse `json:"items"`
	NextCursor string                 `json:"next_cursor"`
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	query, fields := accountListParams(r)
	if len(fields) != 0 {
		s.writeValidation(w, r, fields)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.authOperationTimeout)
	defer cancel()
	page, err := s.accountAdmin.ListAccounts(ctx, query)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	cursor, err := accountNextCursor(page, query.SearchKey)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	caller, _ := accountFrom(r.Context())
	writeJSON(w, r, s.logger, http.StatusOK, accountsResponse{
		Items: adminAccountsDTO(page.Items, caller.ID), NextCursor: cursor,
	})
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !core.ValidID(id) {
		s.writeValidation(w, r, []httputil.FieldError{{
			Field: "id", Code: "invalid", Message: "must be a valid UUID",
		}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.authOperationTimeout)
	defer cancel()
	account, err := s.accountAdmin.GetAdminAccount(ctx, id)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	caller, _ := accountFrom(r.Context())
	writeJSON(w, r, s.logger, http.StatusOK, adminAccountDTO(account, caller.ID))
}

func accountNextCursor(page core.AccountPage, searchKey string) (string, error) {
	if !page.HasMore {
		return "", nil
	}
	if len(page.Items) == 0 {
		return "", core.ErrInvalidArgument
	}
	return encodeAccountCursor(searchKey, page.Items[len(page.Items)-1])
}

func adminAccountsDTO(accounts []core.AdminAccount, callerID string) []adminAccountResponse {
	items := make([]adminAccountResponse, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, adminAccountDTO(account, callerID))
	}
	return items
}

func adminAccountDTO(account core.AdminAccount, callerID string) adminAccountResponse {
	roles := make([]accountRoleAssignmentResponse, 0, len(account.Roles))
	for _, role := range account.Roles {
		roles = append(roles, accountRoleAssignmentResponse{Name: role.Name, Source: role.Source})
	}
	mediaUsers := make([]accountLinkedMediaUserResponse, 0, len(account.MediaUsers))
	for _, link := range account.MediaUsers {
		mediaUsers = append(mediaUsers, accountLinkedMediaUserResponse{
			MediaServerID: link.MediaServerID, MediaServerName: link.MediaServerName,
			MediaUserID: link.MediaUserID, Username: link.Username, Suppressed: link.SuppressedAt != nil,
		})
	}
	return adminAccountResponse{
		ID: account.ID, Username: account.Username, CreatedAt: account.CreatedAt,
		SignInMethod: account.SignInMethod, Roles: roles, MediaUsers: mediaUsers,
		IsSelf: account.ID == callerID,
	}
}
