package store

import (
	"strings"
	"testing"
)

// A password must be long enough to resist guessing and short enough for
// bcrypt, which refuses more than 72 bytes.
func TestValidatePassword(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		password string
		want     string
	}{
		{"", "password must be at least 12 chars"},
		{strings.Repeat("a", 11), "password must be at least 12 chars"},
		{strings.Repeat("a", 12), ""},
		{strings.Repeat("a", 72), ""},
		{strings.Repeat("a", 73), "password must be at most 72 bytes"},
		{strings.Repeat("é", 37), "password must be at most 72 bytes"},
	} {
		err := ValidatePassword(tt.password)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tt.want {
			t.Errorf("ValidatePassword(%d bytes) = %q, want %q", len(tt.password), got, tt.want)
		}
	}
}
