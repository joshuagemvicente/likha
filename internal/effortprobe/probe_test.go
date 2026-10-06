package effortprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"likha/internal/model"
)

// gpt-5.5 publishes none, low, medium, high, xhigh on openai in the bundled
// catalog, so a documented-shape run sends 1 + 5 + 1 = 7 requests.
const probeModel = "gpt-5.5"

var gpt55Levels = []string{"none", "low", "medium", "high", "xhigh"}

// honorTokens scales reasoning with the level, as a provider that honors
// effort would.
var honorTokens = map[string]int64{"": 900, "none": 0, "low": 200, "medium": 900, "high": 2400, "xhigh": 6000, "max": 9000}

func honors(effort string, carried bool) reply {
	if carried && effort == ControlLevel {
		return reply{status: http.StatusBadRequest, errBody: `{"error":{"message":"invalid reasoning_effort"}}`}
	}
	tokens := honorTokens[effort]
	return reply{reasoning: tokens, completion: tokens + 40}
}

func rejectsLevel(level string) behavior {
	return func(effort string, carried bool) reply {
		if carried && effort == level {
			return reply{status: http.StatusBadRequest, errBody: `{"error":"unsupported value"}`}
		}
		return honors(effort, carried)
	}
}

func ignores(string, bool) reply { return reply{reasoning: 500, completion: 540} }

func maps(effort string, carried bool) reply {
	if effort == ControlLevel {
		effort = "medium"
	}
	tokens := honorTokens[effort]
	return reply{reasoning: tokens, completion: tokens + 40}
}

func validatesFlat(effort string, carried bool) reply {
	if effort == ControlLevel {
		return reply{status: http.StatusBadRequest, errBody: `{"error":"bad effort"}`}
	}
	return reply{reasoning: 500, completion: 540}
}

func noUsage(string, bool) reply { return reply{noUsage: true} }

func default400(effort string, carried bool) reply {
	if !carried || effort != "none" {
		return reply{status: http.StatusBadRequest, errBody: `{"error":"tools need reasoning effort none"}`}
	}
	return reply{reasoning: 0, completion: 30}
}

// probeRun is one finished run with everything it printed.
type probeRun struct {
	report   Report
	table    string
	progress string
	posts    []string
}

func runFake(t *testing.T, provider, modelID string, opts Options, behave behavior, tweak func(*Plan)) probeRun {
	t.Helper()
	fake := newFakeProvider(t, behave)
	p, ok := model.LookupProvider(provider)
	if !ok {
		t.Fatalf("unknown provider %s", provider)
	}
	opts.Endpoint = fake.endpoint()
	opts.Secrets = []string{testKey}
	plan, err := NewPlan(p, modelID, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if tweak != nil {
		tweak(&plan)
	}
	client, err := model.New(fake.endpoint(), modelID, testKey)
	if err != nil {
		t.Fatal(err)
	}
	return runPlan(t, client, plan, fake)
}

func runPlan(t *testing.T, client *model.Client, plan Plan, fake *fakeProvider) probeRun {
	t.Helper()
	providers := slices.Clone(model.Providers)
	var progress, table bytes.Buffer
	report, err := Run(context.Background(), client, plan, Progress{W: &progress})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteTable(&table, report); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(providers, model.Providers) {
		t.Fatal("the probe changed the provider table")
	}
	run := probeRun{report: report, table: table.String(), progress: progress.String(), posts: fake.recorded()}
	assertNoSecret(t, run, testKey, "access-secret-value", "refresh-secret-value")
	return run
}

func assertNoSecret(t *testing.T, run probeRun, secrets ...string) {
	t.Helper()
	encoded, err := json.Marshal(run.report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		for name, text := range map[string]string{"report": string(encoded), "table": run.table, "progress": run.progress} {
			if strings.Contains(text, secret) {
				t.Fatalf("%s contains a credential:\n%s", name, text)
			}
		}
	}
}

func verdicts(rows []Row) []Verdict {
	out := make([]Verdict, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Verdict)
	}
	return out
}

func topPosts(levels ...string) []string {
	posts := []string{"none"}
	for _, level := range levels {
		posts = append(posts, "reasoning_effort="+level)
	}
	return append(posts, "reasoning_effort="+ControlLevel)
}

func TestRunHonoringProviderIsEligible(t *testing.T) {
	run := runFake(t, "openai", probeModel, Options{}, honors, nil)
	if want := topPosts(gpt55Levels...); !slices.Equal(run.posts, want) {
		t.Fatalf("posts = %v, want %v", run.posts, want)
	}
	wantSent := []string{"none"}
	for _, level := range append(slices.Clone(gpt55Levels), ControlLevel) {
		wantSent = append(wantSent, `"reasoning_effort":"`+level+`"`)
	}
	for i, row := range run.report.Rows {
		if row.Sent != wantSent[i] {
			t.Fatalf("row %d sent %q, want %q", i+1, row.Sent, wantSent[i])
		}
	}
	want := []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictValidated}
	if got := verdicts(run.report.Rows); !slices.Equal(got, want) {
		t.Fatalf("verdicts = %v, want %v", got, want)
	}
	shape := run.report.Summary.Shapes[0]
	if !shape.Eligible || shape.Finding != FindingVaried || shape.Signal != SignalReasoning {
		t.Fatalf("summary = %+v", shape)
	}
	if !strings.Contains(run.table, "Eligible to enable reasoning_effort for openai: yes (endpoint override: not evidence for openai)") {
		t.Fatalf("table lacks the eligibility line:\n%s", run.table)
	}
	first := run.report.Rows[1]
	if !first.ToolCalled || !first.AnswerCorrect || first.ReasoningTokens == nil || *first.ReasoningTokens != 0 {
		t.Fatalf("row = %+v", first)
	}
	if first.CostSource != costEstimated || first.CostUSD == nil {
		t.Fatalf("row cost = %v %s, want a catalog estimate", first.CostUSD, first.CostSource)
	}
	if !strings.Contains(run.table, disclaimer) {
		t.Fatal("table lacks the heuristic disclaimer")
	}
}

func TestRunVerdictsAgainstFakeProviders(t *testing.T) {
	cases := []struct {
		name         string
		behave       behavior
		want         []Verdict
		finding      Finding
		eligible     bool
		reasonPart   string
		notesOnLevel string
	}{
		{
			name: "rejects xhigh", behave: rejectsLevel("xhigh"),
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictRejected, VerdictValidated},
			finding: FindingVaried, reasonPart: "xhigh rejected; a catalog override must remove it first",
		},
		{
			name: "ignores the field", behave: ignores,
			want:    []Verdict{VerdictBaseline, VerdictIgnoredSuspected, VerdictIgnoredSuspected, VerdictIgnoredSuspected, VerdictIgnoredSuspected, VerdictIgnoredSuspected, VerdictUnvalidated},
			finding: FindingFlat, reasonPart: "none ignored-suspected",
		},
		{
			name: "maps unknown values", behave: maps,
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictUnvalidated},
			finding: FindingVaried, eligible: true, notesOnLevel: noteMapped,
		},
		{
			name: "validates but stays flat", behave: validatesFlat,
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictValidated},
			finding: FindingFlat, eligible: true, notesOnLevel: noteValidate,
		},
		{
			name: "reports no usage", behave: noUsage,
			want:    []Verdict{VerdictBaseline, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictAccepted, VerdictUnvalidated},
			finding: FindingUnmeasured, reasonPart: "levels unmeasured and the control was unvalidated", notesOnLevel: noteMapped,
		},
		{
			name: "Default rejected, none succeeds", behave: default400,
			want:    []Verdict{VerdictFailed, VerdictAccepted, VerdictInconclusive, VerdictInconclusive, VerdictInconclusive, VerdictInconclusive, VerdictInconclusive},
			finding: FindingUnmeasured, reasonPart: "Default was failed", notesOnLevel: noteDefault,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := runFake(t, "openai", probeModel, Options{}, tc.behave, nil)
			if want := topPosts(gpt55Levels...); !slices.Equal(run.posts, want) {
				t.Fatalf("posts = %v, want %v", run.posts, want)
			}
			if got := verdicts(run.report.Rows); !slices.Equal(got, tc.want) {
				t.Fatalf("verdicts = %v, want %v", got, tc.want)
			}
			shape := run.report.Summary.Shapes[0]
			if shape.Finding != tc.finding || shape.Eligible != tc.eligible || !strings.Contains(shape.Eligibility, tc.reasonPart) {
				t.Fatalf("summary = %+v", shape)
			}
			if tc.notesOnLevel != "" && !slices.Contains(run.report.Rows[1].Notes, tc.notesOnLevel) {
				t.Fatalf("level notes = %v, want %q", run.report.Rows[1].Notes, tc.notesOnLevel)
			}
			if !strings.Contains(run.table, disclaimer) || !strings.Contains(run.table, shape.Eligibility) {
				t.Fatalf("table lacks the summary or disclaimer:\n%s", run.table)
			}
		})
	}
}

func TestRunNoUsageChargesAssumedTokens(t *testing.T) {
	run := runFake(t, "openai", probeModel, Options{}, noUsage, nil)
	totals := run.report.Summary.Totals
	if totals.CompletionTokens != 7*3_000 || !totals.PartlyEstimated {
		t.Fatalf("totals = %+v, want 21,000 estimated tokens", totals)
	}
	if row := run.report.Rows[1]; row.CompletionTokens != nil || row.ReasoningTokens != nil || !row.BudgetEstimated {
		t.Fatalf("row = %+v", row)
	}
	if !strings.Contains(run.table, "completion not reported") {
		t.Fatalf("table does not mark missing counts:\n%s", run.table)
	}
}

func TestRunRateLimitIsInconclusiveAndNotRetried(t *testing.T) {
	behave := func(effort string, carried bool) reply {
		if effort == "medium" {
			return reply{status: http.StatusTooManyRequests, errBody: `{"error":"slow down"}`}
		}
		return honors(effort, carried)
	}
	run := runFake(t, "openai", probeModel, Options{}, behave, nil)
	if want := topPosts(gpt55Levels...); !slices.Equal(run.posts, want) {
		t.Fatalf("posts = %v, want exactly one request per row %v", run.posts, want)
	}
	row := run.report.Rows[3]
	if row.Level != "medium" || row.Verdict != VerdictInconclusive || row.HTTPStatus != http.StatusTooManyRequests || row.Reason != "HTTP 429" {
		t.Fatalf("row = %+v", row)
	}
	if run.report.Summary.Shapes[0].Eligible {
		t.Fatal("an inconclusive published level must block eligibility")
	}
}

func TestRunUnauthorizedStopsTheRun(t *testing.T) {
	behave := func(effort string, carried bool) reply {
		if effort == "low" {
			return reply{status: http.StatusUnauthorized, errBody: `{"error":"revoked"}`}
		}
		return honors(effort, carried)
	}
	run := runFake(t, "openai", probeModel, Options{}, behave, nil)
	if want := []string{"none", "reasoning_effort=none", "reasoning_effort=low"}; !slices.Equal(run.posts, want) {
		t.Fatalf("posts = %v, want %v", run.posts, want)
	}
	if !run.report.Stop.Failed() || run.report.Stop.Cause != StopUnauthorized {
		t.Fatalf("stop = %+v", run.report.Stop)
	}
	for _, row := range run.report.Rows[3:] {
		if row.Outcome != OutcomeSkipped || row.Verdict != VerdictInconclusive || !strings.HasPrefix(row.Reason, "skipped: ") {
			t.Fatalf("row after the stop = %+v", row)
		}
	}
	if run.report.Sent() != 3 {
		t.Fatalf("sent = %d, want 3", run.report.Sent())
	}
}

func TestRunDefaultFailureStopsTheRun(t *testing.T) {
	behave := func(effort string, carried bool) reply {
		if !carried {
			return reply{status: http.StatusInternalServerError, errBody: "upstream down"}
		}
		return honors(effort, carried)
	}
	run := runFake(t, "openai", probeModel, Options{}, behave, nil)
	if want := []string{"none"}; !slices.Equal(run.posts, want) {
		t.Fatalf("posts = %v, want only Default", run.posts)
	}
	if run.report.Stop == nil || run.report.Stop.Cause != StopDefaultFailed || !run.report.Stop.Failed() {
		t.Fatalf("stop = %+v", run.report.Stop)
	}
	if run.report.Rows[0].Verdict != VerdictInconclusive {
		t.Fatalf("Default verdict = %s", run.report.Rows[0].Verdict)
	}
}

func TestRunTimeoutIsInconclusive(t *testing.T) {
	behave := func(effort string, carried bool) reply {
		if effort == "high" {
			return reply{stall: true}
		}
		return honors(effort, carried)
	}
	run := runFake(t, "openai", probeModel, Options{}, behave, func(p *Plan) { p.Caps.RequestTimeout = 300 * time.Millisecond })
	row := run.report.Rows[4]
	if row.Level != "high" || row.Outcome != OutcomeTimeout || row.Verdict != VerdictInconclusive || !row.BudgetEstimated {
		t.Fatalf("row = %+v", row)
	}
	if len(run.posts) != 7 {
		t.Fatalf("posts = %v, want the run to continue after a timeout", run.posts)
	}
}

func TestRunTokenBudgetSkipsTheRest(t *testing.T) {
	behave := func(string, bool) reply { return reply{reasoning: 19_000, completion: 20_000} }
	run := runFake(t, "openai", probeModel, Options{}, behave, nil)
	if len(run.posts) != 2 {
		t.Fatalf("posts = %v, want two before the 40,000-token budget", run.posts)
	}
	if run.report.Stop == nil || run.report.Stop.Cause != StopBudget || run.report.Stop.Failed() {
		t.Fatalf("stop = %+v", run.report.Stop)
	}
	for _, row := range run.report.Rows[2:] {
		if row.Outcome != OutcomeSkipped || !strings.Contains(row.Reason, "budget of 40,000 reached") {
			t.Fatalf("row = %+v", row)
		}
	}
}

func TestRunEchoedKeyIsRedacted(t *testing.T) {
	behave := func(effort string, carried bool) reply {
		if effort == "low" {
			body := `{"error":{"message":"bad key ` + testKey + `","url":"https://x.test/?key=` + testKey + `","header":"Authorization: Bearer ` + testKey + `"}}`
			return reply{status: http.StatusBadRequest, errBody: body}
		}
		return honors(effort, carried)
	}
	run := runFake(t, "openai", probeModel, Options{}, behave, nil)
	row := run.report.Rows[2]
	if row.Verdict != VerdictRejected || !strings.Contains(row.Error, "[redacted]") {
		t.Fatalf("row = %+v", row)
	}
	// runFake already asserted the key is absent from table, progress and report.
}

func TestRunRelayProbesBothShapes(t *testing.T) {
	// opencode-go documents no shape and publishes low, high, max for
	// deepseek-v4-flash: 1 + 2 × (3 + 1) = 9 requests, Default once.
	run := runFake(t, "opencode-go", "deepseek-v4-flash", Options{Wire: "both"}, honors, nil)
	want := []string{
		"none",
		"reasoning_effort=low", "reasoning_effort=high", "reasoning_effort=max", "reasoning_effort=" + ControlLevel,
		"reasoning.effort=low", "reasoning.effort=high", "reasoning.effort=max", "reasoning.effort=" + ControlLevel,
	}
	if !slices.Equal(run.posts, want) {
		t.Fatalf("posts = %v, want %v", run.posts, want)
	}
	if got := run.report.Rows[5].Sent; got != `"reasoning":{"effort":"low"}` {
		t.Fatalf("object shape sent %q", got)
	}
	if len(run.report.Summary.Shapes) != 2 || run.report.Summary.Recommended != "reasoning_effort" {
		t.Fatalf("summary = %+v", run.report.Summary)
	}
}

func TestRunChatGPTUsesResponsesReasoningObject(t *testing.T) {
	fake := newFakeProvider(t, honors)
	client := chatGPTClient(t, fake, probeModel)
	p, _ := model.LookupProvider("chatgpt")
	plan, err := NewPlan(p, probeModel, nil, Options{Levels: []string{"low", "high"}, Secrets: []string{"access-secret-value", "refresh-secret-value"}})
	if err != nil {
		t.Fatal(err)
	}
	run := runPlan(t, client, plan, fake)
	want := []string{"none", "reasoning.effort=low", "reasoning.effort=high", "reasoning.effort=" + ControlLevel}
	if !slices.Equal(run.posts, want) {
		t.Fatalf("posts = %v, want %v", run.posts, want)
	}
	low := run.report.Rows[1]
	if low.Sent != `"reasoning":{"effort":"low"}` || low.Published || low.ReasoningTokens == nil || *low.ReasoningTokens != 200 {
		t.Fatalf("low row = %+v", low)
	}
	if !slices.Contains(low.Notes, noteUnpub) {
		t.Fatalf("unpublished level lacks its note: %v", low.Notes)
	}
	// Unpublished levels never count toward eligibility: even with the
	// control validated, a run on unpublished levels only can support a
	// catalog override, never enabling (decision 13).
	s := run.report.Summary.Shapes[0]
	if s.Shape != "reasoning.effort" || s.Control != VerdictValidated || s.Eligible || run.report.Summary.Recommended != "" {
		t.Fatalf("summary = %+v, recommended %q", s, run.report.Summary.Recommended)
	}
	if !strings.Contains(s.Eligibility, "no published level was probed") || strings.Contains(run.table, "Recommended shape") {
		t.Fatalf("eligibility = %q\n%s", s.Eligibility, run.table)
	}
	if !strings.Contains(run.table, "cost: plan allowance") {
		t.Fatalf("ChatGPT rows must not show a per-token charge:\n%s", run.table)
	}
}

func TestRunInterruptedMarksTheReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	behave := func(effort string, carried bool) reply {
		if effort == "low" {
			cancel()
			return reply{stall: true}
		}
		return honors(effort, carried)
	}
	fake := newFakeProvider(t, behave)
	p, _ := model.LookupProvider("openai")
	plan, err := NewPlan(p, probeModel, nil, Options{Endpoint: fake.endpoint()})
	if err != nil {
		t.Fatal(err)
	}
	client, err := model.New(fake.endpoint(), probeModel, testKey)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(ctx, client, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Interrupted || report.Stop.Cause != StopInterrupted || report.Sent() != 3 {
		t.Fatalf("interrupted = %t, stop = %+v, sent = %d", report.Interrupted, report.Stop, report.Sent())
	}
	if got := fake.recorded(); len(got) != 3 {
		t.Fatalf("posts after Ctrl+C = %v", got)
	}
}

// Every request carries the same task and tool; only the effort field
// differs (spec point 9).
func TestRunRequestsDifferOnlyInEffort(t *testing.T) {
	check := func(t *testing.T, fake *fakeProvider, wantRequests int) {
		t.Helper()
		bodies := fake.bodiesWithoutEffort()
		if len(bodies) != wantRequests {
			t.Fatalf("bodies = %d, want %d", len(bodies), wantRequests)
		}
		for i, body := range bodies {
			if body != bodies[0] {
				t.Fatalf("request %d differs beyond effort:\n%s\nvs\n%s", i+1, body, bodies[0])
			}
		}
		if !strings.Contains(bodies[0], answerTool) || !strings.Contains(bodies[0], "prime numbers below 60") {
			t.Fatalf("body lacks the probe task or tool: %s", bodies[0])
		}
	}
	t.Run("chat completions", func(t *testing.T) {
		fake := newFakeProvider(t, honors)
		p, _ := model.LookupProvider("opencode-go")
		plan, err := NewPlan(p, "deepseek-v4-flash", nil, Options{Endpoint: fake.endpoint()})
		if err != nil {
			t.Fatal(err)
		}
		client, err := model.New(fake.endpoint(), "deepseek-v4-flash", testKey)
		if err != nil {
			t.Fatal(err)
		}
		runPlan(t, client, plan, fake)
		check(t, fake, 9)
	})
	t.Run("responses", func(t *testing.T) {
		fake := newFakeProvider(t, honors)
		client := chatGPTClient(t, fake, probeModel)
		p, _ := model.LookupProvider("chatgpt")
		plan, err := NewPlan(p, probeModel, nil, Options{Levels: []string{"low", "high"}})
		if err != nil {
			t.Fatal(err)
		}
		runPlan(t, client, plan, fake)
		check(t, fake, 4)
	})
}

func TestRunNeverChangesTheCallersClient(t *testing.T) {
	fake := newFakeProvider(t, honors)
	client, err := model.New(fake.endpoint(), probeModel, testKey)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := model.LookupProvider("openai")
	plan, err := NewPlan(p, probeModel, nil, Options{Endpoint: fake.endpoint()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), client, plan, nil); err != nil {
		t.Fatal(err)
	}
	if client.Effort() != "" {
		t.Fatalf("caller's client now sends effort %q", client.Effort())
	}
	if _, ok := client.LastRequest(); ok {
		t.Fatal("probe requests ran on the caller's client instead of forks")
	}
}

// A reply that is only a tool call, with hidden reasoning, is the expected
// answer to the probe task; its first tool-call delta is the first token
// (spec point 11).
func TestRunToolCallOnlyReplyRecordsFirstToken(t *testing.T) {
	toolOnly := func(effort string, carried bool) reply {
		answer := honors(effort, carried)
		answer.toolOnly = true
		return answer
	}
	chatGPTFake := newFakeProvider(t, toolOnly)
	chatGPTPlan, err := NewPlan(lookupProvider("chatgpt"), probeModel, nil, Options{Levels: []string{"low", "high"}})
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string]probeRun{
		"chat completions": runFake(t, "openai", probeModel, Options{}, toolOnly, nil),
		"responses":        runPlan(t, chatGPTClient(t, chatGPTFake, probeModel), chatGPTPlan, chatGPTFake),
	}
	for name, run := range runs {
		t.Run(name, func(t *testing.T) {
			answeredRows := 0
			for _, row := range run.report.Rows {
				if row.Outcome != OutcomeOK {
					continue
				}
				answeredRows++
				if !row.ToolCalled || row.FirstTokenMS == nil || row.TotalMS == nil || *row.FirstTokenMS > *row.TotalMS {
					t.Fatalf("row %d: tool %t, first token %v, total %v", row.Index, row.ToolCalled, row.FirstTokenMS, row.TotalMS)
				}
			}
			if answeredRows == 0 {
				t.Fatalf("no row was answered:\n%s", run.table)
			}
		})
	}
}
