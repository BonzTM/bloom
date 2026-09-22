package core

import (
	"errors"
	"strings"
	"testing"
)

func TestUsernameKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{name: "lowercase", input: "Alice-01", want: "alice-01"},
		{name: "width mapping", input: "Ａlice", want: "alice"},
		{name: "unicode letters", input: "Élodie", want: "élodie"},
		{name: "combining form", input: "E\u0301lodie", want: "élodie"},
		{name: "greek sigma", input: "Σigma", want: "σigma"},
		{name: "greek final sigma preserved", input: "ςigma", want: "ςigma"},
		{name: "sharp s preserved", input: "Straße", want: "straße"},
		{name: "sharp s is not ss", input: "STRASSE", want: "strasse"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := UsernameKey(testCase.input)
			if err != nil || got != testCase.want {
				t.Fatalf("UsernameKey(%q) = %q, %v; want %q", testCase.input, got, err, testCase.want)
			}
		})
	}
}

func TestUsernameKeyRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input string
		errorValue  error
	}{
		{name: "malformed utf8", input: string([]byte{0xff, 'a', 'b'}), errorValue: ErrInvalidText},
		{name: "too short", input: "ab", errorValue: ErrInvalidUsername},
		{name: "too long", input: strings.Repeat("a", MaxUsernameCharacters+1), errorValue: ErrInvalidUsername},
		{name: "leading separator", input: "-alice", errorValue: ErrInvalidUsername},
		{name: "trailing separator", input: "alice_", errorValue: ErrInvalidUsername},
		{name: "space", input: "alice smith", errorValue: ErrInvalidUsername},
		{name: "punctuation", input: "alice@example", errorValue: ErrInvalidUsername},
		{name: "precis disallowed compatibility rune", input: "ali²ce", errorValue: ErrInvalidUsername},
		{name: "precis bidi violation", input: "שalice", errorValue: ErrInvalidUsername},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := UsernameKey(testCase.input); !errors.Is(err, testCase.errorValue) {
				t.Fatalf("UsernameKey(%q) = %v, want %v", testCase.input, err, testCase.errorValue)
			}
		})
	}
}

func TestUsernameKeyIsIdempotent(t *testing.T) {
	t.Parallel()
	first, err := UsernameKey("Ｅ\u0301lodie-01")
	if err != nil {
		t.Fatalf("first UsernameKey: %v", err)
	}
	second, err := UsernameKey(first)
	if err != nil || second != first {
		t.Fatalf("second UsernameKey = %q, %v; want %q", second, err, first)
	}
}

func TestCanonicalUsernameUsesUsernameKey(t *testing.T) {
	t.Parallel()
	got, err := CanonicalUsername("ＡLICE")
	if err != nil || got != "alice" {
		t.Fatalf("CanonicalUsername = %q, %v; want alice, nil", got, err)
	}
}
