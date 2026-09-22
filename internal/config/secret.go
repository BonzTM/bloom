package config

import "log/slog"

// redacted is the only representation a Secret ever renders as.
const redacted = "[redacted]"

// Secret wraps sensitive bytes so they cannot leak through any formatting path:
// fmt verbs, slog attributes, JSON encoding, and error messages all see
// "[redacted]". The value is reachable only through Bytes, which callers use at
// the single point of consumption (key derivation), never for logging.
//
// The zero value is an empty secret.
type Secret struct {
	value []byte
}

// NewSecret wraps b. The slice is copied so later mutation by the caller cannot
// change the secret.
func NewSecret(b []byte) Secret {
	cp := make([]byte, len(b))
	copy(cp, b)
	return Secret{value: cp}
}

// Bytes returns a copy of the secret material.
func (s Secret) Bytes() []byte {
	cp := make([]byte, len(s.value))
	copy(cp, s.value)
	return cp
}

// Len reports the secret length in bytes without exposing the material.
func (s Secret) Len() int { return len(s.value) }

// String implements fmt.Stringer and always redacts.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer (%#v) and always redacts.
func (s Secret) GoString() string { return "config.Secret{" + redacted + "}" }

// LogValue implements slog.LogValuer and always redacts.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText implements encoding.TextMarshaler (used by encoding/json) and
// always redacts.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
