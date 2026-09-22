package core

import (
	"errors"
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	matched, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !matched {
		t.Fatal("VerifyPassword = false, want true")
	}
	if PasswordHashNeedsUpgrade(hash) {
		t.Fatal("new hash needs an upgrade")
	}
}

func TestPasswordHashRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("right-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	matched, err := VerifyPassword(hash, "wrong-password")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if matched {
		t.Fatal("VerifyPassword = true, want false")
	}
}

func TestPasswordHashRejectsEmptyPassword(t *testing.T) {
	t.Parallel()

	if _, err := HashPassword(""); !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("HashPassword(empty) = %v, want ErrEmptyPassword", err)
	}
	if _, err := VerifyPassword("invalid", ""); !errors.Is(err, ErrEmptyPassword) {
		t.Fatalf("VerifyPassword(empty) = %v, want ErrEmptyPassword", err)
	}
}

func TestPasswordHashRejectsOversizedPassword(t *testing.T) {
	t.Parallel()

	password := strings.Repeat("x", MaxPasswordBytes+1)
	if _, err := HashPassword(password); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("HashPassword(oversized) = %v, want ErrPasswordTooLong", err)
	}
}

func TestValidateNewPasswordPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{name: "too short", password: strings.Repeat("x", MinNewPasswordCharacters-1), wantErr: ErrPasswordTooShort},
		{name: "minimum", password: strings.Repeat("x", MinNewPasswordCharacters)},
		{name: "common password", password: "password", wantErr: ErrPasswordCompromised},
		{name: "malformed UTF-8", password: string([]byte{'v', 'a', 'l', 'i', 'd', 0xff, 'p', 'a', 's', 's', 'w', 'o', 'r', 'd', 'x'}), wantErr: ErrInvalidText},
		{name: "valid", password: "violet-orbit-lantern-73"},
		{name: "maximum", password: strings.Repeat("x", MaxPasswordCharacters)},
		{name: "too many characters", password: strings.Repeat("x", MaxPasswordCharacters+1), wantErr: ErrPasswordTooLong},
		{name: "too many bytes", password: strings.Repeat("界", MaxPasswordBytes/len("界")+1), wantErr: ErrPasswordTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateNewPassword(tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateNewPassword() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestPasswordHashRejectsTampering(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(hash, "$")
	parts[4] = "not-base64!"
	if _, err := VerifyPassword(strings.Join(parts, "$"), "password"); !errors.Is(err, ErrInvalidPasswordHash) {
		t.Fatalf("VerifyPassword(tampered) = %v, want ErrInvalidPasswordHash", err)
	}
}

func TestParsePasswordHashParameters(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	params, salt, digest, err := parsePasswordHash(hash)
	if err != nil {
		t.Fatalf("parsePasswordHash: %v", err)
	}
	if params != currentPasswordParams {
		t.Errorf("params = %+v, want %+v", params, currentPasswordParams)
	}
	if len(salt) != passwordSaltBytes {
		t.Errorf("salt length = %d, want %d", len(salt), passwordSaltBytes)
	}
	if len(digest) != int(currentPasswordParams.keyBytes) {
		t.Errorf("digest length = %d, want %d", len(digest), currentPasswordParams.keyBytes)
	}
}

func TestParsePasswordHashRejectsUnsafeParameters(t *testing.T) {
	t.Parallel()

	cases := []string{
		"$argon2id$v=16$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$YWJj",
		"$argon2id$v=19$m=999999999,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$YWJj",
		"$argon2id$v=19$m=19456,t=0,p=1$c2FsdHNhbHRzYWx0c2FsdA$YWJj",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$YWJj",
	}
	for _, encoded := range cases {
		if _, _, _, err := parsePasswordHash(encoded); !errors.Is(err, ErrInvalidPasswordHash) {
			t.Errorf("parsePasswordHash(%q) = %v, want ErrInvalidPasswordHash", encoded, err)
		}
	}
}

func TestParsePasswordHashLengthBoundary(t *testing.T) {
	t.Parallel()

	tooLong := strings.Repeat("x", MaxPasswordHashBytes+1)
	if _, _, _, err := parsePasswordHash(tooLong); !errors.Is(err, ErrInvalidPasswordHash) {
		t.Fatalf("parsePasswordHash(max+1) = %v, want ErrInvalidPasswordHash", err)
	}
	atLimit := strings.Repeat("x", MaxPasswordHashBytes)
	if _, _, _, err := parsePasswordHash(atLimit); !errors.Is(err, ErrInvalidPasswordHash) {
		t.Fatalf("parsePasswordHash(max) = %v, want bounded rejection", err)
	}
}

func TestPasswordHashSupportedProfiles(t *testing.T) {
	t.Parallel()

	password := "legacy-secret"
	salt := []byte("0123456789abcdef")
	for _, params := range supportedPasswordProfiles {
		digest := derivePassword(password, salt, params)
		encoded := encodePasswordHash(params, salt, digest)
		matched, err := VerifyPassword(encoded, password)
		if err != nil || !matched {
			t.Errorf("VerifyPassword(%+v) = %v, %v", params, matched, err)
		}
		if got := PasswordHashNeedsUpgrade(encoded); got != (params != currentPasswordParams) {
			t.Errorf("PasswordHashNeedsUpgrade(%+v) = %v", params, got)
		}
	}
}

func TestParsePasswordHashRejectsUnsupportedBoundaries(t *testing.T) {
	t.Parallel()

	salt := []byte("0123456789abcdef")
	cases := []passwordParams{
		{version: argonVersion - 1, memory: passwordMemoryKiB, time: passwordIterations, threads: passwordThreads, keyBytes: passwordKeyBytes},
		{version: argonVersion + 1, memory: passwordMemoryKiB, time: passwordIterations, threads: passwordThreads, keyBytes: passwordKeyBytes},
		{version: argonVersion, memory: passwordMemoryKiB - 1, time: passwordIterations, threads: passwordThreads, keyBytes: passwordKeyBytes},
		{version: argonVersion, memory: passwordMemoryKiB + 1, time: passwordIterations, threads: passwordThreads, keyBytes: passwordKeyBytes},
		{version: argonVersion, memory: passwordMemoryKiB, time: passwordIterations - 1, threads: passwordThreads, keyBytes: passwordKeyBytes},
		{version: argonVersion, memory: passwordMemoryKiB, time: passwordIterations + 1, threads: passwordThreads, keyBytes: passwordKeyBytes},
		{version: argonVersion, memory: passwordMemoryKiB, time: passwordIterations, threads: passwordThreads - 1, keyBytes: passwordKeyBytes},
		{version: argonVersion, memory: passwordMemoryKiB, time: passwordIterations, threads: passwordThreads + 1, keyBytes: passwordKeyBytes},
		{version: argonVersion, memory: passwordMemoryKiB, time: passwordIterations, threads: passwordThreads, keyBytes: passwordLegacyKeyBytes - 1},
		{version: argonVersion, memory: passwordMemoryKiB, time: passwordIterations, threads: passwordThreads, keyBytes: passwordKeyBytes + 1},
	}
	for _, params := range cases {
		digest := make([]byte, params.keyBytes)
		encoded := encodePasswordHash(params, salt, digest)
		if _, _, _, err := parsePasswordHash(encoded); !errors.Is(err, ErrInvalidPasswordHash) {
			t.Errorf("parsePasswordHash(%+v) = %v, want ErrInvalidPasswordHash", params, err)
		}
	}
}

func FuzzParsePasswordHash(f *testing.F) {
	hash, err := HashPassword("seed-password")
	if err != nil {
		f.Fatalf("HashPassword: %v", err)
	}
	f.Add(hash)
	f.Add("")
	f.Add(strings.Repeat("x", MaxPasswordHashBytes+1))
	f.Fuzz(func(t *testing.T, encoded string) {
		_, _, _, err := parsePasswordHash(encoded)
		if err != nil && !errors.Is(err, ErrInvalidPasswordHash) {
			t.Fatalf("parsePasswordHash returned unexpected error: %v", err)
		}
	})
}
