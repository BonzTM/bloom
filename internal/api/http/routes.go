package http

import (
	"fmt"
	"net/http"
	"strings"

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
	method, path string
	access       routeAccess
	permission   core.Permission
	sessions     bool
	authRequired bool
	snapshot     bool
	oidc         bool
	handler      apiHandler
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
		handler = s.RequirePermission(permission)(handler)
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
	return handler, nil
}
