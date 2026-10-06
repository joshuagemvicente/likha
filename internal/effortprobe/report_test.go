package effortprobe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sampleReport(t *testing.T) Report {
	t.Helper()
	run := runFake(t, "openai", probeModel, Options{}, rejectsLevel("xhigh"), nil)
	run.report.StartedAt = time.Date(2026, 10, 6, 9, 30, 5, 0, time.UTC)
	return run.report
}

func TestWriteReportRoundTripAndModes(t *testing.T) {
	state := t.TempDir()
	report := sampleReport(t)
	path, err := WriteReport(state, report)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(state, "effort-probes", "20261006T093005Z-openai-gpt-5.5.json"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %v, want 0600", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v, want 0700", dir.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Rows, report.Rows) || !reflect.DeepEqual(decoded.Summary, report.Summary) {
		t.Fatal("report rows or summary changed in the round trip")
	}
	if decoded.FormatVersion != formatVersion || decoded.TaskVersion != probeTaskVersion || decoded.Caps.RequestTimeoutSeconds != 180 {
		t.Fatalf("report header = %+v", decoded)
	}
	for _, key := range []string{`"format_version"`, `"likha_version"`, `"started_at"`, `"endpoint_override"`, `"level_source"`, `"rows"`, `"summary"`, `"interrupted"`, `"disclaimer"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("report lacks %s", key)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files were left behind: %v", entries)
	}
}

func TestWriteReportNeverOverwrites(t *testing.T) {
	state := t.TempDir()
	report := sampleReport(t)
	first, err := WriteReport(state, report)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	report.Model = probeModel // same second, same name
	report.Interrupted = true
	second, err := WriteReport(state, report)
	if err != nil {
		t.Fatal(err)
	}
	if second == first || !strings.HasSuffix(second, "-gpt-5.5-2.json") {
		t.Fatalf("second report path = %s (first %s)", second, first)
	}
	after, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the first report was overwritten")
	}
	third, err := WriteReport(state, report)
	if err != nil || !strings.HasSuffix(third, "-3.json") {
		t.Fatalf("third report = %s, %v", third, err)
	}
}

func TestWriteReportFailureIsReturned(t *testing.T) {
	state := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(state, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteReport(state, Report{Provider: "openai", Model: "m"}); err == nil {
		t.Fatal("writing under a file succeeded")
	}
}

func TestFileNamePart(t *testing.T) {
	cases := []struct{ in, want string }{
		{"gpt-5.5", "gpt-5.5"},
		{"anthropic/claude-3.5-sonnet", "anthropic-claude-3.5-sonnet"},
		{"anthropic.claude-3-5-haiku-20241022-v1:0", "anthropic.claude-3-5-haiku-20241022-v1-0"},
		{"../../etc", "..-..-etc"},
		{"model with space", "model-with-space"},
	}
	for _, tc := range cases {
		if got := fileNamePart(tc.in); got != tc.want {
			t.Fatalf("fileNamePart(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRedact(t *testing.T) {
	const key = "sk-test/secret+key=="
	cases := []struct {
		name string
		text string
	}{
		{"json error body", `model HTTP 400: {"error":{"message":"Incorrect API key provided: ` + key + `"}}`},
		{"url", `see https://example.test/v1?key=sk-test%2Fsecret%2Bkey%3D%3D for details`},
		{"quoted string", `bad credential "` + key + `"`},
		{"authorization header", `echo: Authorization: Bearer abc.def-ghi`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redact(tc.text, key)
			if strings.Contains(got, key) || strings.Contains(got, "sk-test%2Fsecret") || strings.Contains(got, "abc.def-ghi") {
				t.Fatalf("redact left a credential: %q", got)
			}
			if !strings.Contains(got, "[redacted]") {
				t.Fatalf("redact = %q, want a [redacted] marker", got)
			}
		})
	}
}

func TestRedactTruncatesOnCharacterBoundary(t *testing.T) {
	long := strings.Repeat("é", 1000) // 2,000 bytes
	got := redact(long)
	if len(got) > maxErrorText || !strings.HasPrefix(long, got) || len(got) < maxErrorText-1 {
		t.Fatalf("truncated to %d bytes", len(got))
	}
}
