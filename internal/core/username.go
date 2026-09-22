package core

import (
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/secure/precis"
)

const (
	// MinUsernameCharacters is the shortest canonical username Bloom accepts.
	MinUsernameCharacters = 3
	// MaxUsernameCharacters bounds account lookups and rate-limit keys.
	MaxUsernameCharacters = 64
	// MaxSubmittedUsernameCharacters bounds work before PRECIS canonicalization.
	MaxSubmittedUsernameCharacters = 256
)

// ErrInvalidUsername reports a username outside Bloom's canonical syntax.
var ErrInvalidUsername = errors.New("invalid username")

// UsernameKey returns the PRECIS UsernameCaseMapped comparison key used for
// lookup, uniqueness, rate limiting, and audit correlation.
func UsernameKey(username string) (string, error) {
	if !utf8.ValidString(username) {
		return "", ErrInvalidText
	}
	key, err := precis.UsernameCaseMapped.CompareKey(username)
	if err != nil {
		return "", fmt.Errorf("PRECIS UsernameCaseMapped profile: %w", errors.Join(ErrInvalidUsername, err))
	}
	if err := validateUsernameKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// CanonicalUsername returns the display form stored for an account. Bloom's
// current display form is the same PRECIS value as its comparison key.
func CanonicalUsername(username string) (string, error) { return UsernameKey(username) }

func validateUsernameKey(key string) error {
	length := utf8.RuneCountInString(key)
	if length < MinUsernameCharacters || length > MaxUsernameCharacters {
		return fmt.Errorf("%w: length must be between %d and %d characters", ErrInvalidUsername, MinUsernameCharacters, MaxUsernameCharacters)
	}
	for index, value := range key {
		if !usernameRuneAllowed(value) {
			return fmt.Errorf("%w: character at byte %d is not allowed", ErrInvalidUsername, index)
		}
	}
	first, _ := utf8.DecodeRuneInString(key)
	last, _ := utf8.DecodeLastRuneInString(key)
	if usernameSeparator(first) || usernameSeparator(last) {
		return fmt.Errorf("%w: separators cannot lead or trail", ErrInvalidUsername)
	}
	return nil
}

func usernameRuneAllowed(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || usernameSeparator(value)
}

func usernameSeparator(value rune) bool {
	return value == '.' || value == '_' || value == '-'
}
