package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrOIDCRejected classifies a terminal OpenID Connect callback rejection.
	ErrOIDCRejected = errors.New("OpenID Connect sign-in rejected")
	// ErrOIDCProviderUnavailable classifies a transient provider dependency failure.
	ErrOIDCProviderUnavailable = errors.New("OpenID Connect provider unavailable")
	// ErrOIDCProvisioningDisabled reports an unknown verified identity when JIT
	// account provisioning is not explicitly enabled.
	ErrOIDCProvisioningDisabled = errors.New("OpenID Connect account provisioning disabled")
)

const (
	// MaxOIDCIssuerBytes bounds the verified issuer identifier at every boundary.
	MaxOIDCIssuerBytes = 2048
	// MaxOIDCSubjectBytes bounds the verified provider-local subject identifier.
	MaxOIDCSubjectBytes = 512
)

// OIDCRejection is a terminal credential or verified-claim rejection.
type OIDCRejection struct {
	Stage string
	Err   error
}

func (e *OIDCRejection) Error() string { return "OpenID Connect " + e.Stage + " rejected" }
func (e *OIDCRejection) Unwrap() error { return e.Err }

// Is classifies the error as ErrOIDCRejected.
func (e *OIDCRejection) Is(target error) bool { return target == ErrOIDCRejected }

// OIDCDependencyError is a transient failure of discovery, token exchange, or JWKS.
type OIDCDependencyError struct {
	Operation  string
	RetryAfter time.Duration
	Err        error
}

func (e *OIDCDependencyError) Error() string { return "OpenID Connect " + e.Operation + " unavailable" }
func (e *OIDCDependencyError) Unwrap() error { return e.Err }

// Is classifies the error as ErrOIDCProviderUnavailable.
func (e *OIDCDependencyError) Is(target error) bool { return target == ErrOIDCProviderUnavailable }

// OIDCClaims contains only the verified claims Bloom consumes.
type OIDCClaims struct {
	Issuer     string
	Subject    string
	Username   string
	RoleValues []string
}

// OIDCProvider is the protocol seam consumed by the HTTP sign-in flow.
type OIDCProvider interface {
	AuthorizationURL(state, nonce, codeChallenge string) string
	Exchange(ctx context.Context, code, verifier, nonce string) (OIDCClaims, error)
}

// OIDCIdentity records one external subject linked to a Bloom account.
type OIDCIdentity struct {
	AccountID     string
	Provider      string
	Issuer        string
	Subject       string
	UsernameClaim string
	MappedRoles   []string
}

// OIDCSignIn carries one verified identity into the atomic account-link store.
type OIDCSignIn struct {
	Provider      string
	Issuer        string
	Subject       string
	UsernameClaim string
	MappedRoles   []string
	DefaultRole   string
	AutoProvision bool
	Now           time.Time
}

// OIDCSignInResult reports the resolved Bloom principal.
type OIDCSignInResult struct {
	Account     Account
	Provisioned bool
	// AddedRoles and RemovedRoles report committed OIDC-source changes only.
	// Manual grants are independent and never appear in these deltas.
	AddedRoles   []string
	RemovedRoles []string
}

// OIDCAccountStore atomically resolves, provisions, links, and synchronizes an
// OIDC identity. The implementation owns transaction boundaries.
type OIDCAccountStore interface {
	SignInOIDC(ctx context.Context, signIn OIDCSignIn) (OIDCSignInResult, error)
}

// OIDCFlowStore atomically claims the persisted pre-authentication session
// that owns one OIDC flow. The token is the opaque browser session token.
type OIDCFlowStore interface {
	ClaimOIDCFlow(ctx context.Context, token string) (bool, error)
}

// MapOIDCRoles maps verified claim values to sorted unique Bloom role names.
func MapOIDCRoles(values []string, roleMap map[string]string) []string {
	roles := make([]string, 0, len(values))
	for _, value := range values {
		if role := roleMap[value]; role != "" {
			roles = append(roles, role)
		}
	}
	slices.Sort(roles)
	return slices.Compact(roles)
}

// OIDCUsername derives a canonical Bloom username from an external claim. A
// positive collision number appends a stable short subject suffix.
func OIDCUsername(claim, identityKey string, collision int) (string, error) {
	base := sanitizeOIDCUsername(claim)
	if base == "" {
		base = "oidc-user"
	}
	suffix := ""
	if collision > 0 {
		digest := sha256.Sum256([]byte(identityKey))
		suffix = "-" + hex.EncodeToString(digest[:4])
		if collision > 1 {
			suffix += fmt.Sprintf("-%d", collision)
		}
	}
	base = truncateRunes(base, MaxUsernameCharacters-utf8.RuneCountInString(suffix))
	base = strings.Trim(base, "._-")
	return CanonicalUsername(base + suffix)
}

func sanitizeOIDCUsername(value string) string {
	var builder strings.Builder
	separator := false
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
			separator = false
			continue
		}
		if builder.Len() > 0 && !separator {
			builder.WriteByte('-')
			separator = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
