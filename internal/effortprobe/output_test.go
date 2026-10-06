package effortprobe

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"likha/internal/model"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden table files")

// fixClock replaces everything a real run makes time- or port-dependent, so
// the table of a real fake-provider run can be compared byte for byte.
func fixClock(r *Report) {
	r.StartedAt = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	r.EndedAt = r.StartedAt.Add(42 * time.Second)
	r.Endpoint = "http://127.0.0.1:8080"
	for i := range r.Rows {
		row := &r.Rows[i]
		if row.HeadersMS != nil {
			row.HeadersMS = int64Ptr(120)
		}
		if row.FirstTokenMS != nil {
			row.FirstTokenMS = int64Ptr(450)
		}
		if row.TotalMS != nil {
			row.TotalMS = int64Ptr(1500)
		}
	}
}

func TestTableGolden(t *testing.T) {
	cases := []struct {
		name   string
		behave behavior
	}{
		{"honors", honors},
		{"ignores", ignores},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := runFake(t, "openai", probeModel, Options{}, tc.behave, nil)
			fixClock(&run.report)
			var table bytes.Buffer
			if err := WriteTable(&table, run.report); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", tc.name+".golden")
			if *updateGolden {
				if err := os.WriteFile(path, table.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if table.String() != string(want) {
				t.Fatalf("table differs from %s (run with -update to inspect):\n%s", path, table.String())
			}
			if strings.ContainsRune(table.String(), '\x1b') {
				t.Fatal("table contains a terminal escape sequence")
			}
		})
	}
}

func TestWritePlanNamesEverythingBeforeConsent(t *testing.T) {
	p, _ := model.LookupProvider("openai")
	plan, err := NewPlan(p, probeModel, nil, Options{Endpoint: "http://127.0.0.1:9/v1", Levels: []string{"low", "turbo"}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WritePlan(&out, plan, Estimate(plan)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Provider: openai",
		"Model:    gpt-5.5",
		"Endpoint: http://127.0.0.1:9 (endpoint override)",
		`Wire shape: reasoning_effort (field "reasoning_effort": "<level>")`,
		"Levels (--probe-levels): low, turbo (unpublished)",
		`Requests: 4 (1 Default, 2 levels, 1 control "likha-probe-invalid")`,
		"Estimated cost: about $",
		"assumes up to 3,000 output tokens per request at $30.00/M",
		"at most 17 requests; no new request after 40,000 completion tokens",
		"180 s per request",
		"A single request's output is not capped by the probe; the budget stops new requests only.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("plan lacks %q:\n%s", want, out.String())
		}
	}
}

func TestWritePlanClaudeCompatNoteComesFirst(t *testing.T) {
	p, _ := model.LookupProvider("claude")
	plan, err := NewPlan(p, "claude-opus-5-5", nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WritePlan(&out, plan, Estimate(plan)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), claudeCompatNote+"\n") {
		t.Fatalf("plan does not start with the compatibility note:\n%s", out.String())
	}
}
