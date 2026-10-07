package redact

import (
	"strings"
	"testing"
)

func TestSecrets(t *testing.T) {
	const key = "sk-test/key+1"
	cases := []struct {
		name   string
		in     string
		leaked string
	}{
		{"raw", "bad key " + key, key},
		{"query escaped", "url?key=sk-test%2Fkey%2B1", "sk-test%2Fkey%2B1"},
		{"path escaped", "path/sk-test%2Fkey+1", "sk-test%2Fkey+1"},
		{"bearer", "Authorization: Bearer abc.def-123", "abc.def-123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Secrets(tc.in, key)
			if strings.Contains(got, tc.leaked) {
				t.Fatalf("Secrets(%q) = %q, still contains %q", tc.in, got, tc.leaked)
			}
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("Secrets(%q) = %q, want a redaction marker", tc.in, got)
			}
		})
	}
	if got := Secrets("plain text", ""); got != "plain text" {
		t.Fatalf("empty secret changed text: %q", got)
	}
}
