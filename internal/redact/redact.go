// Package redact removes credentials from text Likha prints or saves.
package redact

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// bearerPattern finds an authorization value a provider may echo.
var bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`)

// Secrets replaces every secret in text, in its raw, URL-escaped and
// JSON-escaped forms, plus any bearer authorization value, with
// "[redacted]". Empty secrets are ignored.
func Secrets(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for _, form := range secretForms(secret) {
			text = strings.ReplaceAll(text, form, "[redacted]")
		}
	}
	return bearerPattern.ReplaceAllString(text, "Bearer [redacted]")
}

func secretForms(secret string) []string {
	forms := []string{secret, url.QueryEscape(secret), url.PathEscape(secret)}
	if quoted, err := json.Marshal(secret); err == nil {
		forms = append(forms, strings.Trim(string(quoted), `"`))
	}
	return forms
}
