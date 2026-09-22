package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewID returns a random RFC 9562 version-4 UUID as its canonical 36-character
// lowercase string. It is the identifier format for every Bloom entity: a
// string primary key stores identically on SQLite and PostgreSQL, so no
// engine-specific id type leaks into the domain (ADR 0004). 122 bits of
// crypto/rand entropy make collisions negligible without any coordination.
//
// A hand-rolled generator is used instead of a dependency because the format is
// sixteen random bytes with two bit-fields set; a module would add nothing.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 9562 variant
	return formatUUID(b), nil
}

// formatUUID renders 16 bytes as 8-4-4-4-12 lowercase hex.
func formatUUID(b [16]byte) string {
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}
