package core

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var (
	// ErrEmptyPassword reports that a password was empty.
	ErrEmptyPassword = errors.New("password must not be empty")
	// ErrInvalidPasswordHash reports malformed or unsafe stored hash parameters.
	ErrInvalidPasswordHash = errors.New("invalid password hash")
	// ErrPasswordTooLong reports a password above the bounded authentication
	// input limit.
	ErrPasswordTooLong = errors.New("password is too long")
	// ErrPasswordTooShort reports a new password below the account-creation
	// minimum. Existing credentials remain verifiable after policy changes.
	ErrPasswordTooShort = errors.New("password must be at least 15 characters")
	// ErrPasswordCompromised reports a password present in the offline denylist.
	ErrPasswordCompromised = errors.New("password is commonly used or compromised")
	// ErrInvalidText reports malformed UTF-8 at a text boundary.
	ErrInvalidText = errors.New("text must be valid UTF-8")
)

// compromisedPasswordData contains the first 3,000 entries from SecLists'
// xato-net-10-million-passwords-10000.txt, normalized into a case-insensitive
// set at startup. Source:
// https://github.com/danielmiessler/SecLists/blob/master/Passwords/Common-Credentials/xato-net-10-million-passwords-10000.txt
// SecLists is MIT licensed, Copyright (c) 2018 Daniel Miessler.
//
//go:embed compromised_passwords.txt
var compromisedPasswordData string

var compromisedPasswords = newCompromisedPasswordSet(compromisedPasswordData)

const (
	passwordSaltBytes        = 16
	argonVersion             = argon2.Version
	passwordMemoryKiB        = 19 * 1024
	passwordIterations       = 2
	passwordThreads          = 1
	passwordLegacyKeyBytes   = 16
	passwordKeyBytes         = 32
	compromisedPasswordCount = 3000
	// MaxPasswordHashBytes bounds stored PHC input before splitting or decoding.
	MaxPasswordHashBytes = 128
	// MaxPasswordBytes bounds password input retained and processed by Argon2id.
	MaxPasswordBytes = 4 * MaxPasswordCharacters
	// MaxPasswordCharacters matches JSON Schema maxLength semantics.
	MaxPasswordCharacters = 1024
	// MinNewPasswordCharacters is the minimum accepted for a new credential.
	MinNewPasswordCharacters = 15
)

type passwordParams struct {
	version  int
	memory   uint32
	time     uint32
	threads  uint8
	keyBytes uint32
}

type passwordDeriveFunc func(string, []byte, passwordParams) ([]byte, error)

// currentPasswordParams follows OWASP's Argon2id minimum of 19 MiB memory,
// two iterations, and one lane. See
// https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html#argon2id.
// The PHC string stores every parameter and the Argon2 version, so a future
// parameter set can be detected and upgraded after a successful login.
var (
	currentPasswordParams = passwordParams{
		version: argonVersion, memory: passwordMemoryKiB, time: passwordIterations,
		threads: passwordThreads, keyBytes: passwordKeyBytes,
	}
	supportedPasswordProfiles = [...]passwordParams{
		currentPasswordParams,
		{
			version: argonVersion, memory: passwordMemoryKiB, time: passwordIterations,
			threads: passwordThreads, keyBytes: passwordLegacyKeyBytes,
		},
	}
)

// ValidateNewPassword enforces the length and offline-compromise policy for new credentials.
func ValidateNewPassword(password string) error {
	if password == "" {
		return ErrEmptyPassword
	}
	if !utf8.ValidString(password) {
		return ErrInvalidText
	}
	if _, compromised := compromisedPasswords[normalizeCompromisedPassword(password)]; compromised {
		return ErrPasswordCompromised
	}
	if utf8.RuneCountInString(password) < MinNewPasswordCharacters {
		return ErrPasswordTooShort
	}
	if passwordTooLong(password) {
		return ErrPasswordTooLong
	}
	return nil
}

// HashPassword returns an Argon2id PHC string using a fresh 16-byte salt.
func HashPassword(password string) (string, error) {
	return hashPassword(password, realPasswordDerivation)
}

func hashPassword(password string, derive passwordDeriveFunc) (string, error) {
	if password == "" {
		return "", ErrEmptyPassword
	}
	if !utf8.ValidString(password) {
		return "", ErrInvalidText
	}
	if passwordTooLong(password) {
		return "", ErrPasswordTooLong
	}
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	digest, err := derive(password, salt, currentPasswordParams)
	if err != nil {
		return "", fmt.Errorf("derive password hash: %w", err)
	}
	return encodePasswordHash(currentPasswordParams, salt, digest), nil
}

// VerifyPassword compares password with an encoded Argon2id hash in constant
// time after strictly parsing and bounding its parameters.
func VerifyPassword(encoded, password string) (bool, error) {
	return verifyPassword(encoded, password, realPasswordDerivation)
}

func verifyPassword(encoded, password string, derive passwordDeriveFunc) (bool, error) {
	if password == "" {
		return false, ErrEmptyPassword
	}
	if !utf8.ValidString(password) {
		return false, ErrInvalidText
	}
	if passwordTooLong(password) {
		return false, ErrPasswordTooLong
	}
	params, salt, want, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	got, err := derive(password, salt, params)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// PasswordHashNeedsUpgrade reports whether encoded differs from the current
// versioned parameter set. Malformed hashes also require replacement.
func PasswordHashNeedsUpgrade(encoded string) bool {
	params, _, _, err := parsePasswordHash(encoded)
	return err != nil || params != currentPasswordParams
}

func derivePassword(password string, salt []byte, params passwordParams) []byte {
	return argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, params.keyBytes)
}

func realPasswordDerivation(password string, salt []byte, params passwordParams) ([]byte, error) {
	return derivePassword(password, salt, params), nil
}

func encodePasswordHash(params passwordParams, salt, digest []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		params.version, params.memory, params.time, params.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	)
}

func parsePasswordHash(encoded string) (passwordParams, []byte, []byte, error) {
	if len(encoded) > MaxPasswordHashBytes {
		return passwordParams{}, nil, nil, ErrInvalidPasswordHash
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return passwordParams{}, nil, nil, ErrInvalidPasswordHash
	}
	params, err := parsePasswordParams(parts[2], parts[3])
	if err != nil {
		return passwordParams{}, nil, nil, err
	}
	salt, err := decodePasswordPart(parts[4], passwordSaltBytes)
	if err != nil {
		return passwordParams{}, nil, nil, err
	}
	digest, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return passwordParams{}, nil, nil, ErrInvalidPasswordHash
	}
	switch len(digest) {
	case passwordLegacyKeyBytes:
		params.keyBytes = passwordLegacyKeyBytes
	case passwordKeyBytes:
		params.keyBytes = passwordKeyBytes
	default:
		return passwordParams{}, nil, nil, ErrInvalidPasswordHash
	}
	if !supportedPasswordParams(params) {
		return passwordParams{}, nil, nil, ErrInvalidPasswordHash
	}
	return params, salt, digest, nil
}

func parsePasswordParams(versionPart, paramsPart string) (passwordParams, error) {
	var params passwordParams
	var threads uint32
	if _, err := fmt.Sscanf(versionPart, "v=%d", &params.version); err != nil {
		return passwordParams{}, ErrInvalidPasswordHash
	}
	if _, err := fmt.Sscanf(paramsPart, "m=%d,t=%d,p=%d", &params.memory, &params.time, &threads); err != nil {
		return passwordParams{}, ErrInvalidPasswordHash
	}
	if versionPart != fmt.Sprintf("v=%d", params.version) ||
		paramsPart != fmt.Sprintf("m=%d,t=%d,p=%d", params.memory, params.time, threads) {
		return passwordParams{}, ErrInvalidPasswordHash
	}
	if threads > 255 {
		return passwordParams{}, ErrInvalidPasswordHash
	}
	params.threads = uint8(threads)
	return params, nil
}

func supportedPasswordParams(params passwordParams) bool {
	for _, supported := range supportedPasswordProfiles {
		if params == supported {
			return true
		}
	}
	return false
}

func decodePasswordPart(encoded string, want int) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != want {
		return nil, ErrInvalidPasswordHash
	}
	return decoded, nil
}

func consumePasswordWork(password string, derive passwordDeriveFunc) error {
	salt := make([]byte, passwordSaltBytes)
	want := make([]byte, currentPasswordParams.keyBytes)
	got, err := derive(password, salt, currentPasswordParams)
	if err != nil {
		return err
	}
	_ = subtle.ConstantTimeCompare(got, want)
	return nil
}

func passwordTooLong(password string) bool {
	return len(password) > MaxPasswordBytes || utf8.RuneCountInString(password) > MaxPasswordCharacters
}

func newCompromisedPasswordSet(data string) map[string]struct{} {
	passwords := make(map[string]struct{}, compromisedPasswordCount)
	remaining := data
	for range compromisedPasswordCount {
		password, rest, found := strings.Cut(remaining, "\n")
		if !found {
			panic("embedded compromised-password list has too few entries")
		}
		if normalized := normalizeCompromisedPassword(password); normalized != "" {
			passwords[normalized] = struct{}{}
		}
		remaining = rest
	}
	if remaining != "" {
		panic("embedded compromised-password list has too many entries")
	}
	return passwords
}

func normalizeCompromisedPassword(password string) string {
	return strings.ToLower(strings.TrimSpace(password))
}
