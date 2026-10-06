package effortprobe

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// formatVersion changes whenever a report field changes meaning.
const formatVersion = 1

// Cost sources a row can carry.
const (
	costReported  = "reported"
	costEstimated = "estimated"
	costUnknown   = "unknown"
)

// maxErrorText caps provider error text in rows (spec point 11).
const maxErrorText = 1 << 10

// ReportCaps are the caps a run used, in report units.
type ReportCaps struct {
	MaxRequests           int   `json:"max_requests"`
	CompletionTokenBudget int64 `json:"completion_token_budget"`
	RequestTimeoutSeconds int64 `json:"request_timeout_seconds"`
	AssumedOutputTokens   int64 `json:"assumed_output_tokens_per_request"`
}

// Report is the JSON record of one run (spec point 24). It never holds an
// API key, OAuth token, authorization header or request body beyond each
// row's effort fragment.
type Report struct {
	FormatVersion    int        `json:"format_version"`
	LikhaVersion     string     `json:"likha_version"`
	StartedAt        time.Time  `json:"started_at"`
	EndedAt          time.Time  `json:"ended_at"`
	Provider         string     `json:"provider"`
	Model            string     `json:"model"`
	Endpoint         string     `json:"endpoint"`
	EndpointOverride bool       `json:"endpoint_override"`
	Shapes           []string   `json:"shapes"`
	Levels           []Level    `json:"levels"`
	PublishedLevels  []string   `json:"published_levels"`
	LevelSource      string     `json:"level_source"`
	Caps             ReportCaps `json:"caps"`
	TaskVersion      int        `json:"task_version"`
	Rows             []Row      `json:"rows"`
	Summary          Summary    `json:"summary"`
	Stop             *Stop      `json:"stopped_early,omitempty"`
	Interrupted      bool       `json:"interrupted"`
	Disclaimer       string     `json:"disclaimer"`
}

func newReport(plan Plan, start time.Time) Report {
	shapes := make([]string, 0, len(plan.Shapes))
	for _, s := range plan.Shapes {
		shapes = append(shapes, s.Label())
	}
	return Report{
		FormatVersion: formatVersion, LikhaVersion: plan.LikhaVersion, StartedAt: start.UTC(),
		Provider: plan.Provider.Name, Model: plan.Model, Endpoint: plan.Endpoint, EndpointOverride: plan.EndpointOverride,
		Shapes: shapes, Levels: plan.Levels, PublishedLevels: plan.Published, LevelSource: plan.LevelSource,
		Caps: ReportCaps{
			MaxRequests: plan.Caps.MaxRequests, CompletionTokenBudget: plan.Caps.TokenBudget,
			RequestTimeoutSeconds: int64(plan.Caps.RequestTimeout / time.Second), AssumedOutputTokens: plan.Caps.AssumedOutput,
		},
		TaskVersion: probeTaskVersion, Disclaimer: disclaimer,
	}
}

// Sent counts the rows whose request reached the transport. A report is
// written whenever it is above zero (spec point 23).
func (r Report) Sent() int {
	sent := 0
	for _, row := range r.Rows {
		if row.Sent != "" {
			sent++
		}
	}
	return sent
}

// bearerPattern finds an authorization value a provider may echo.
var bearerPattern = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`)

// Redact removes every secret from text, in its raw, URL-escaped and
// JSON-escaped forms, plus any bearer authorization value, then cuts the
// result to 1 KiB on a character boundary (tasks T11). It is exported so
// provider errors printed before consent are cleaned the same way as rows.
func Redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for _, form := range secretForms(secret) {
			text = strings.ReplaceAll(text, form, "[redacted]")
		}
	}
	text = bearerPattern.ReplaceAllString(text, "Bearer [redacted]")
	return truncate(text, maxErrorText)
}

func secretForms(secret string) []string {
	forms := []string{secret, url.QueryEscape(secret), url.PathEscape(secret)}
	if quoted, err := json.Marshal(secret); err == nil {
		forms = append(forms, strings.Trim(string(quoted), `"`))
	}
	return forms
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// reportDir is the private directory reports live in.
const reportDir = "effort-probes"

// maxNameAttempts bounds the numeric suffixes tried for one report name.
const maxNameAttempts = 1000

// WriteReport saves r under <stateDir>/effort-probes/ (directory 0700, file
// 0600) and returns its path. It never overwrites a report: the file is
// written under a temporary name and hard-linked to the first free final
// name, adding -2, -3, … when a report of the same second exists.
func WriteReport(stateDir string, r Report) (string, error) {
	dir := filepath.Join(stateDir, reportDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create report directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("protect report directory: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode report: %w", err)
	}
	tmp, err := writeTemp(dir, append(data, '\n'))
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	base := reportBaseName(r)
	for attempt := 1; attempt <= maxNameAttempts; attempt++ {
		name := base
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", base, attempt)
		}
		path := filepath.Join(dir, name+".json")
		err := os.Link(tmp, path)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("save report %s: %w", path, err)
		}
	}
	return "", fmt.Errorf("save report: %d reports named %s already exist", maxNameAttempts, base)
}

func writeTemp(dir string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create report file: %w", err)
	}
	name := file.Name()
	if err := writeAndClose(file, data); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func writeAndClose(file *os.File, data []byte) error {
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("protect report file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write report file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close report file: %w", err)
	}
	return nil
}

// reportBaseName is <UTC time>-<provider>-<model> without the extension.
func reportBaseName(r Report) string {
	return r.StartedAt.UTC().Format("20060102T150405Z") + "-" + fileNamePart(r.Provider) + "-" + fileNamePart(r.Model)
}

// fileNamePart keeps letters, digits, '.', '_' and '-'; anything else, such
// as the '/' of an OpenRouter model ID, becomes '-'.
func fileNamePart(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, s)
}
