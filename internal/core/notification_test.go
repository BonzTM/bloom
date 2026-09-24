package core

import (
	"errors"
	"testing"
)

func TestValidateNotificationRecipientsRejectsCaseInsensitiveDuplicates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		recipients []string
		duplicate  bool
	}{
		{name: "distinct", recipients: []string{"Alice@example.com", "bob@example.com"}},
		{name: "local part case", recipients: []string{"Alice@example.com", "alice@example.com"}, duplicate: true},
		{name: "domain case", recipients: []string{"alice@EXAMPLE.com", "alice@example.COM"}, duplicate: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateNotificationRecipients(testCase.recipients)
			if errors.Is(err, ErrDuplicateNotificationRecipient) != testCase.duplicate {
				t.Fatalf("ValidateNotificationRecipients() error = %v, duplicate = %v", err, testCase.duplicate)
			}
		})
	}
}
