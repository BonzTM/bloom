package http

import (
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/BonzTM/bloom/internal/core"
)

type routeAccess uint8

const (
	routePublic routeAccess = iota + 1
	routeAuthenticated
	routePermission
)

type apiHandler func(*Server, http.ResponseWriter, *http.Request)

type apiRoute struct {
	method, path   string
	access         routeAccess
	permission     core.Permission
	anyPermissions []core.Permission
	sessions       bool
	authRequired   bool
	snapshot       bool
	oidc           bool
	handler        apiHandler
}

var apiRouteInventory = []apiRoute{
	{method: http.MethodGet, path: "/api/v1/version", access: routePublic, handler: (*Server).handleVersion},
	{method: http.MethodGet, path: "/api/v1/auth/permissions", access: routePublic, handler: (*Server).handlePermissions},
	{method: http.MethodGet, path: "/api/v1/auth/providers", access: routePublic, handler: (*Server).handleAuthProviders},
	{method: http.MethodPost, path: "/api/v1/auth/oidc/start", access: routePublic, sessions: true, authRequired: true, oidc: true, handler: (*Server).handleOIDCStart},
	{method: http.MethodGet, path: "/api/v1/auth/oidc/callback", access: routePublic, sessions: true, authRequired: true, oidc: true, handler: (*Server).handleOIDCCallback},
	{method: http.MethodPost, path: "/api/v1/auth/login", access: routePublic, sessions: true, authRequired: true, handler: (*Server).handleLogin},
	{method: http.MethodPost, path: "/api/v1/auth/logout", access: routeAuthenticated, authRequired: true, handler: (*Server).handleLogout},
	{method: http.MethodGet, path: "/api/v1/auth/me", access: routeAuthenticated, authRequired: true, snapshot: true, handler: (*Server).handleMe},
	{method: http.MethodGet, path: "/api/v1/roles", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true, handler: (*Server).handleRoles},
	{method: http.MethodPost, path: "/api/v1/media-servers", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleCreateMediaServer},
	{method: http.MethodGet, path: "/api/v1/media-servers", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleListMediaServers},
	{method: http.MethodGet, path: "/api/v1/media-servers/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleGetMediaServer},
	{method: http.MethodPost, path: "/api/v1/media-servers/{id}/probe", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleProbeMediaServer},
	{method: http.MethodGet, path: "/api/v1/media-servers/{id}/libraries", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleMediaServerLibraries},
	{method: http.MethodDelete, path: "/api/v1/media-servers/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleDeleteMediaServer},
	{method: http.MethodPost, path: "/api/v1/invites", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true, handler: (*Server).handleCreateInvite},
	{method: http.MethodGet, path: "/api/v1/invites", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true, handler: (*Server).handleListInvites},
	{method: http.MethodGet, path: "/api/v1/invites/{id}", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true, handler: (*Server).handleGetInvite},
	{method: http.MethodDelete, path: "/api/v1/invites/{id}", access: routePermission, permission: core.PermissionUsersInvite, authRequired: true, handler: (*Server).handleRevokeInvite},
	{method: http.MethodGet, path: "/api/v1/invite/{code}", access: routePublic, handler: (*Server).handlePreviewInvite},
	{method: http.MethodPost, path: "/api/v1/invite/{code}/accept", access: routePublic, handler: (*Server).handleAcceptInvite},
	{method: http.MethodGet, path: "/api/v1/playback/now", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true, handler: (*Server).handlePlaybackNow},
	{method: http.MethodGet, path: "/api/v1/playback/history", access: routePermission, permission: core.PermissionStatsReadAll, authRequired: true, handler: (*Server).handlePlaybackHistory},
	{method: http.MethodGet, path: "/api/v1/metadata/search", access: routePermission, permission: core.PermissionRequestsCreate, authRequired: true, handler: (*Server).handleMetadataSearch},
	{method: http.MethodGet, path: "/api/v1/metadata/movies/{id}", access: routePermission, permission: core.PermissionRequestsCreate, authRequired: true, handler: (*Server).handleMetadataMovie},
	{method: http.MethodGet, path: "/api/v1/metadata/series/{id}", access: routePermission, permission: core.PermissionRequestsCreate, authRequired: true, handler: (*Server).handleMetadataSeries},
	{method: http.MethodGet, path: "/api/v1/metadata/providers/tmdb/key", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleMetadataKeyPresence},
	{method: http.MethodPut, path: "/api/v1/metadata/providers/tmdb/key", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleSetMetadataKey},
	{method: http.MethodDelete, path: "/api/v1/metadata/providers/tmdb/key", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleDeleteMetadataKey},
	{method: http.MethodPost, path: "/api/v1/request-profiles", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleCreateRequestProfile},
	{method: http.MethodGet, path: "/api/v1/request-profiles", access: routePermission, anyPermissions: []core.Permission{core.PermissionRequestsCreate, core.PermissionAdminSettings}, authRequired: true, handler: (*Server).handleListRequestProfiles},
	{method: http.MethodPut, path: "/api/v1/request-profiles/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleUpdateRequestProfile},
	{method: http.MethodDelete, path: "/api/v1/request-profiles/{id}", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleDeleteRequestProfile},
	{method: http.MethodPost, path: "/api/v1/requests", access: routePermission, permission: core.PermissionRequestsCreate, authRequired: true, handler: (*Server).handleCreateRequest},
	{method: http.MethodGet, path: "/api/v1/requests", access: routePermission, anyPermissions: []core.Permission{core.PermissionRequestsReadOwn, core.PermissionRequestsApprove}, authRequired: true, handler: (*Server).handleListRequests},
	{method: http.MethodGet, path: "/api/v1/requests/{id}", access: routePermission, anyPermissions: []core.Permission{core.PermissionRequestsReadOwn, core.PermissionRequestsApprove}, authRequired: true, handler: (*Server).handleGetRequest},
	{method: http.MethodPost, path: "/api/v1/requests/{id}/approve", access: routePermission, permission: core.PermissionRequestsApprove, authRequired: true, handler: (*Server).handleApproveRequest},
	{method: http.MethodPost, path: "/api/v1/requests/{id}/decline", access: routePermission, permission: core.PermissionRequestsApprove, authRequired: true, handler: (*Server).handleDeclineRequest},
	{method: http.MethodGet, path: "/api/v1/roles/{id}/request-quota", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true, handler: (*Server).handleGetRoleRequestQuota},
	{method: http.MethodPut, path: "/api/v1/roles/{id}/request-quota", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true, handler: (*Server).handleSetRoleRequestQuota},
	{method: http.MethodDelete, path: "/api/v1/roles/{id}/request-quota", access: routePermission, permission: core.PermissionAdminRoles, authRequired: true, handler: (*Server).handleDeleteRoleRequestQuota},
	{method: http.MethodGet, path: "/api/v1/accounts/{id}/request-quota", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleGetAccountRequestQuota},
	{method: http.MethodPut, path: "/api/v1/accounts/{id}/request-quota", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleSetAccountRequestQuota},
	{method: http.MethodDelete, path: "/api/v1/accounts/{id}/request-quota", access: routePermission, permission: core.PermissionAdminSettings, authRequired: true, handler: (*Server).handleDeleteAccountRequestQuota},
}

func (r apiRoute) usesSessionAccount() bool {
	return r.access == routeAuthenticated || r.access == routePermission
}

func (r apiRoute) validate() (core.CatalogPermission, error) {
	if r.method == "" || r.path == "" || r.handler == nil {
		return core.CatalogPermission{}, fmt.Errorf("route %q is incomplete", r.path)
	}
	if r.access == routePermission {
		if !r.authRequired {
			return core.CatalogPermission{}, fmt.Errorf("permission route %q is not auth-enabled", r.path)
		}
		if len(r.anyPermissions) > 0 {
			if r.permission != "" {
				return core.CatalogPermission{}, fmt.Errorf("route %q mixes permission modes", r.path)
			}
			for _, item := range r.anyPermissions {
				if _, err := core.NewCatalogPermission(item); err != nil {
					return core.CatalogPermission{}, err
				}
			}
			return core.CatalogPermission{}, nil
		}
		return core.NewCatalogPermission(r.permission)
	}
	if r.access != routePublic && r.access != routeAuthenticated {
		return core.CatalogPermission{}, fmt.Errorf("route %q has invalid access classification", r.path)
	}
	if r.permission != "" {
		return core.CatalogPermission{}, fmt.Errorf("route %q has permission outside permission access", r.path)
	}
	if r.sessions && (r.access != routePublic || !r.authRequired) {
		return core.CatalogPermission{}, fmt.Errorf("session route %q is not public and auth-enabled", r.path)
	}
	if r.snapshot && !r.usesSessionAccount() {
		return core.CatalogPermission{}, fmt.Errorf("permission-loading route %q is not authenticated", r.path)
	}
	if r.access == routeAuthenticated && !r.authRequired {
		return core.CatalogPermission{}, fmt.Errorf("authenticated route %q is not auth-enabled", r.path)
	}
	return core.CatalogPermission{}, nil
}

func (s *Server) registerAPIRoutes(mux *http.ServeMux) error {
	handlers := make(map[string][]methodHandler)
	paths := make([]string, 0, len(apiRouteInventory))
	for _, route := range apiRouteInventory {
		handler, err := s.routeHandler(route)
		if err != nil {
			return err
		}
		if route.authRequired && s.loginLimiter == nil {
			continue
		}
		if route.oidc && (s.oidcProvider == nil || s.oidcAccounts == nil || s.oidcFlows == nil || !s.oidcConfig.Enabled) {
			continue
		}
		if strings.HasPrefix(route.path, "/api/v1/media-servers") && (s.mediaServerReader == nil || s.mediaServerManager == nil) {
			continue
		}
		if strings.HasPrefix(route.path, "/api/v1/invite") &&
			(s.inviteReader == nil || s.inviteManager == nil) {
			continue
		}
		if strings.HasPrefix(route.path, "/api/v1/playback") && s.playbackReader == nil {
			continue
		}
		if strings.HasPrefix(route.path, "/api/v1/metadata") && (s.metadataReader == nil || s.metadataManager == nil) {
			continue
		}
		if (strings.HasPrefix(route.path, "/api/v1/request") || strings.Contains(route.path, "/request-quota")) && s.requestService == nil {
			continue
		}
		if _, exists := handlers[route.path]; !exists {
			paths = append(paths, route.path)
		}
		handlers[route.path] = append(handlers[route.path], methodHandler{method: route.method, handler: handler})
	}
	for _, path := range paths {
		mux.Handle(path, s.dispatchMethods(handlers[path]))
	}
	return nil
}

type methodHandler struct {
	method  string
	handler http.Handler
}

func (s *Server) dispatchMethods(handlers []methodHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := make([]string, 0, len(handlers))
		for _, candidate := range handlers {
			allowed = append(allowed, candidate.method)
			if r.Method == candidate.method {
				candidate.handler.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, r, s.logger, errMethodNotAllowed)
	})
}

func (s *Server) routeHandler(route apiRoute) (http.Handler, error) {
	permission, err := route.validate()
	if err != nil {
		return nil, err
	}
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route.handler(s, w, r)
	})
	if route.access == routePermission {
		if len(route.anyPermissions) > 0 {
			handler = s.RequireAnyPermission(route.anyPermissions)(handler)
		} else {
			handler = s.RequirePermission(permission)(handler)
		}
	}
	if route.access == routePermission {
		handler = s.permissionSetMiddleware(handler)
	}
	if route.snapshot {
		handler = s.authorizationSnapshotMiddleware(handler)
	}
	if route.usesSessionAccount() {
		handler = s.sessionHandler(handler, true)
	} else if route.sessions {
		handler = s.sessionHandler(handler, false)
	}
	if strings.HasPrefix(route.path, "/api/v1/media-servers") {
		handler = s.mediaServerOperationMiddleware(handler)
	}
	if strings.HasPrefix(route.path, "/api/v1/invite") {
		handler = s.mediaServerOperationMiddleware(handler)
	}
	if isPublicInviteRoute(route.path) {
		handler = sanitizedInviteTrace(route)(handler)
	}
	if strings.HasPrefix(route.path, "/api/v1/playback") {
		handler = s.mediaServerOperationMiddleware(handler)
	}
	return handler, nil
}

func isPublicInviteRoute(pattern string) bool {
	return pattern == "/api/v1/invite/{code}" || pattern == "/api/v1/invite/{code}/accept"
}

func sanitizedInviteTrace(route apiRoute) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := otel.Tracer("github.com/BonzTM/bloom/internal/api/http").Start(
				ctx, route.method+" "+route.path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attribute.String("http.route", route.path)),
			)
			defer span.End()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
