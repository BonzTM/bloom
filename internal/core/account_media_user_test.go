package core_test

import (
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

func TestValidateAccountMediaUser(t *testing.T) {
	valid := core.AccountMediaUser{
		AccountID: "11111111-1111-4111-8111-111111111111", MediaServerID: "22222222-2222-4222-8222-222222222222",
		MediaUserID: "user-1", Username: "alice", Source: core.AccountMediaUserSourceMatch,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	tests := []struct {
		name   string
		mutate func(*core.AccountMediaUser)
	}{
		{name: "valid"},
		{name: "bad account", mutate: func(v *core.AccountMediaUser) { v.AccountID = "bad" }},
		{name: "empty media user", mutate: func(v *core.AccountMediaUser) { v.MediaUserID = "" }},
		{name: "long media user", mutate: func(v *core.AccountMediaUser) { v.MediaUserID = string(make([]byte, 129)) }},
		{name: "control in media user", mutate: func(v *core.AccountMediaUser) { v.MediaUserID = "bad\x00id" }},
		{name: "long username", mutate: func(v *core.AccountMediaUser) { v.Username = string(make([]byte, 65)) }},
		{name: "bad source", mutate: func(v *core.AccountMediaUser) { v.Source = "other" }},
		{name: "zero created", mutate: func(v *core.AccountMediaUser) { v.CreatedAt = time.Time{} }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := valid
			if testCase.mutate != nil {
				testCase.mutate(&value)
			}
			err := core.ValidateAccountMediaUser(value)
			if (err != nil) != (testCase.mutate != nil) {
				t.Fatalf("ValidateAccountMediaUser() = %v", err)
			}
		})
	}
}
