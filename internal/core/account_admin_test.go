package core

import (
	"errors"
	"strings"
	"testing"
)

func TestAccountSearchKeyNormalizesUsernameSubstring(t *testing.T) {
	t.Parallel()
	got, err := AccountSearchKey("ＬIC")
	if err != nil || got != "lic" {
		t.Fatalf("AccountSearchKey = %q, %v; want lic, nil", got, err)
	}
}

func TestAccountSearchKeyBoundsInputBytes(t *testing.T) {
	t.Parallel()
	for _, value := range []string{strings.Repeat("a", MaxAccountSearchBytes+1), string([]byte{0xff})} {
		if _, err := AccountSearchKey(value); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("AccountSearchKey(%q) = %v, want ErrInvalidArgument", value, err)
		}
	}
}

func TestAccountSearchKeyAcceptsNonUsernameSubstrings(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"alice smith", "%", "_"} {
		if _, err := AccountSearchKey(value); err != nil {
			t.Errorf("AccountSearchKey(%q) = %v, want nil", value, err)
		}
	}
}

func TestSignInMethod(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		hasLocal, hasOIDC bool
		want              AccountSignInMethod
		wantErr           bool
	}{
		{name: "local", hasLocal: true, want: AccountSignInLocal},
		{name: "oidc", hasOIDC: true, want: AccountSignInOIDC},
		{name: "both", hasLocal: true, hasOIDC: true, want: AccountSignInBoth},
		{name: "none", wantErr: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := SignInMethod(testCase.hasLocal, testCase.hasOIDC)
			if got != testCase.want || (err != nil) != testCase.wantErr {
				t.Fatalf("SignInMethod = %q, %v; want %q, error %t", got, err, testCase.want, testCase.wantErr)
			}
		})
	}
}

func TestAccountListQueryValidation(t *testing.T) {
	t.Parallel()
	valid := AccountListQuery{Limit: MaxAccountListPageSize, SearchKey: "alice", After: &AccountListPosition{
		UsernameKey: "alice", ID: "11111111-1111-4111-8111-111111111111",
	}}
	if !valid.Valid() {
		t.Fatal("valid account list query rejected")
	}
	invalid := []AccountListQuery{
		{Limit: 0},
		{Limit: MaxAccountListPageSize + 1},
		{Limit: 1, SearchKey: strings.Repeat("a", maxAccountSearchKeyBytes+1)},
		{Limit: 1, SearchKey: "Alice"},
		{Limit: 1, After: &AccountListPosition{UsernameKey: "Alice", ID: valid.After.ID}},
		{Limit: 1, After: &AccountListPosition{UsernameKey: "alice", ID: "bad"}},
	}
	for _, query := range invalid {
		if query.Valid() {
			t.Errorf("invalid query accepted: %+v", query)
		}
	}
}
