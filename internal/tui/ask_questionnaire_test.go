package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"likha/internal/agent"
	"likha/internal/providers"
	"likha/internal/session"
	"likha/internal/tools"
)

// Questionnaire form (specs/ask-user amendment 2026-10-05): one question per
// page, n of N progress, a Recommended tag that starts focused, muted
// descriptions, ←/→ between pages, per-page Ctrl+S skip, and one submission
// once the last unanswered page is answered or skipped.

func askTestQuestionnaire() tools.AskRequest {
	return tools.AskRequest{Questions: []tools.AskQuestion{
		{Question: "Which database?", Options: []tools.AskOption{
			{Label: "SQLite", Description: "Embedded, no server to run."},
			{Label: "Postgres", Description: "Matches production.", Recommended: true},
		}},
		{Question: "Which port?", Options: []tools.AskOption{
			{Label: "5432"},
			{Label: "6543"},
		}},
		{Question: "Anything else?"},
	}}
}

// askReplies collects every reply the dialog delivers.
type askReplies struct{ got []tools.AskAnswer }

func (r *askReplies) reply(answer tools.AskAnswer) { r.got = append(r.got, answer) }

func openTestAsk(t *testing.T, width int, req tools.AskRequest) (*ui, *askReplies) {
	t.Helper()
	m := toolItemTestUI(t, width)
	replies := &askReplies{}
	m.openAskQuestion(req, "ask_1_1", "call_q", replies.reply)
	if !m.ask.pending || !m.askVisible() {
		t.Fatalf("questionnaire did not open: %+v", m.ask)
	}
	return m, replies
}

func askKey(m *ui, k tea.KeyMsg) { m.Update(k) }

func askType(m *ui, text string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}

func askViewText(m *ui) string { return stripANSI(m.askView()) }

// askLastNote is the newest answer conversation event (a Working… row may
// follow it while the run resumes).
func askLastNote(m *ui) string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if c := m.entries[i].content; strings.HasPrefix(c, "ask answered") || strings.HasPrefix(c, "question skipped") {
			return c
		}
	}
	return ""
}

func TestAskQuestionnaireRecommendedStartsFocused(t *testing.T) {
	m, _ := openTestAsk(t, 100, askTestQuestionnaire())
	if m.ask.page != 0 || m.ask.selected != 1 {
		t.Fatalf("page %d selected %d, want page 0 focused on the recommended option (1)", m.ask.page, m.ask.selected)
	}
	view := askViewText(m)
	if !strings.Contains(view, "> 2. Postgres  Recommended") {
		t.Fatalf("recommended option is not focused and tagged:\n%s", view)
	}
	if strings.Contains(view, "SQLite  Recommended") {
		t.Fatalf("tag on a non-recommended option:\n%s", view)
	}
	// Without a recommended option the first row starts focused.
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.ask.page != 1 || m.ask.selected != 0 {
		t.Fatalf("page %d selected %d, want page 1 on its first row", m.ask.page, m.ask.selected)
	}
	if view := askViewText(m); !strings.Contains(view, "> 1. 5432") || strings.Contains(view, "Recommended") {
		t.Fatalf("page without a recommendation:\n%s", view)
	}
	// A page without options starts on the free-text row.
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.ask.page != 2 || !m.ask.textMode() {
		t.Fatalf("page %d textMode %t, want page 2 on free text", m.ask.page, m.ask.textMode())
	}
}

func TestAskQuestionnaireShowsProgressDescriptionsAndHint(t *testing.T) {
	m, _ := openTestAsk(t, 100, askTestQuestionnaire())
	view := askViewText(m)
	for _, want := range []string{
		"Model question · 1 of 3",
		"Which database?",
		"Embedded, no server to run.",
		"Matches production.",
		"Free text",
		askQuestionnaireHint,
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lacks %q:\n%s", want, view)
		}
	}
	// The description sits under its own option.
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "1. SQLite") {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "Embedded, no server to run.") {
				t.Fatalf("description is not under its option:\n%s", view)
			}
		}
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	if view := askViewText(m); !strings.Contains(view, "Model question · 2 of 3") || !strings.Contains(view, "Which port?") {
		t.Fatalf("second page:\n%s", view)
	}
}

func TestAskQuestionnaireChangeEarlierAnswer(t *testing.T) {
	m, replies := openTestAsk(t, 100, askTestQuestionnaire())
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // Postgres (recommended)
	if m.ask.page != 1 || len(replies.got) != 0 {
		t.Fatalf("after first answer: page %d, %d replies", m.ask.page, len(replies.got))
	}
	// ← returns to the answered page with its answer shown and its focus kept.
	askKey(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.ask.page != 0 || m.ask.selected != 1 {
		t.Fatalf("back on page %d selected %d, want page 0 selected 1", m.ask.page, m.ask.selected)
	}
	if view := askViewText(m); !strings.Contains(view, "Current answer: Postgres") {
		t.Fatalf("revisited page does not show its answer:\n%s", view)
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyUp})
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // SQLite replaces Postgres
	if m.ask.page != 1 {
		t.Fatalf("Enter on a changed answer went to page %d, want the next unanswered (1)", m.ask.page)
	}
	// Free text on page 2, then the last page by free text too.
	askKey(m, tea.KeyMsg{Type: tea.KeyTab})
	askType(m, "6000")
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ask.page != 2 || len(replies.got) != 0 {
		t.Fatalf("after second answer: page %d, %d replies", m.ask.page, len(replies.got))
	}
	askType(m, "no")
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(replies.got) != 1 {
		t.Fatalf("replies = %d, want exactly one submission", len(replies.got))
	}
	want := []tools.AskQuestionAnswer{
		{Text: "SQLite", Choice: true},
		{Text: "6000"},
		{Text: "no"},
	}
	got := replies.got[0]
	if got.Text != "" || got.Skipped || len(got.Answers) != len(want) {
		t.Fatalf("submission = %+v", got)
	}
	for i := range want {
		if got.Answers[i] != want[i] {
			t.Fatalf("answer %d = %+v, want %+v", i, got.Answers[i], want[i])
		}
	}
	if m.ask.pending || m.status != "Answers sent" {
		t.Fatalf("after submit: pending %t status %q", m.ask.pending, m.status)
	}
	// The conversation records each question's answer with the
	// single-question vocabulary that resume reconciliation recognizes.
	note := askLastNote(m)
	for _, line := range []string{
		"ask answered (choice): Which database? → SQLite",
		"ask answered (text): Which port? → 6000",
		"ask answered (text): Anything else? → no",
	} {
		if !strings.Contains(note, line) {
			t.Fatalf("answer note lacks %q:\n%s", line, note)
		}
	}
}

func TestAskQuestionnaireSkipOnePage(t *testing.T) {
	m, replies := openTestAsk(t, 100, askTestQuestionnaire())
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // Postgres
	askKey(m, tea.KeyMsg{Type: tea.KeyCtrlS}) // skip the port
	if m.ask.page != 2 || !m.ask.answers[1].answer.Skipped || len(replies.got) != 0 {
		t.Fatalf("after skip: page %d answers %+v replies %d", m.ask.page, m.ask.answers, len(replies.got))
	}
	askType(m, "ship it")
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(replies.got) != 1 {
		t.Fatalf("replies = %d, want 1", len(replies.got))
	}
	answers := replies.got[0].Answers
	if len(answers) != 3 || answers[0] != (tools.AskQuestionAnswer{Text: "Postgres", Choice: true}) ||
		answers[1] != (tools.AskQuestionAnswer{Skipped: true}) || answers[2] != (tools.AskQuestionAnswer{Text: "ship it"}) {
		t.Fatalf("answers = %+v", answers)
	}
	if note := askLastNote(m); !strings.Contains(note, "question skipped: Which port?") {
		t.Fatalf("note lacks the skipped question:\n%s", note)
	}
}

// Skipping a page in the middle and answering later pages leaves the skipped
// one answered-as-skipped; skipping an earlier page out of order still waits
// for every unanswered page.
func TestAskQuestionnaireSkipWaitsForUnansweredPages(t *testing.T) {
	m, replies := openTestAsk(t, 100, askTestQuestionnaire())
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	askKey(m, tea.KeyMsg{Type: tea.KeyCtrlS}) // skip the last page first
	if m.ask.page != 0 || len(replies.got) != 0 {
		t.Fatalf("skip on the last page: page %d replies %d, want the first unanswered page", m.ask.page, len(replies.got))
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ask.page != 1 || len(replies.got) != 0 {
		t.Fatalf("page %d replies %d", m.ask.page, len(replies.got))
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(replies.got) != 1 || len(replies.got[0].Answers) != 3 || !replies.got[0].Answers[2].Skipped {
		t.Fatalf("replies = %+v", replies.got)
	}
}

func TestAskQuestionnaireAllSkipped(t *testing.T) {
	m, replies := openTestAsk(t, 100, askTestQuestionnaire())
	for range 3 {
		askKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	}
	if len(replies.got) != 1 {
		t.Fatalf("replies = %d, want 1", len(replies.got))
	}
	got := replies.got[0]
	// The UI reports every page skipped; the backend turns that into the
	// all-skipped refusal.
	if got.Skipped || got.Text != "" || len(got.Answers) != 3 {
		t.Fatalf("all-skipped submission = %+v", got)
	}
	for i, answer := range got.Answers {
		if answer != (tools.AskQuestionAnswer{Skipped: true}) {
			t.Fatalf("answer %d = %+v, want skipped", i, answer)
		}
	}
	if m.status != "Questions skipped" || m.ask.pending {
		t.Fatalf("status %q pending %t", m.status, m.ask.pending)
	}
}

func TestAskQuestionnaireSubmitsExactlyOnce(t *testing.T) {
	m, replies := openTestAsk(t, 100, askTestQuestionnaire())
	draft := "half-typed draft"
	m.input = []rune(draft)
	m.queue = []string{"held"}
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(replies.got) != 0 {
		t.Fatalf("submitted before the last page: %+v", replies.got)
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if len(replies.got) != 1 {
		t.Fatalf("replies = %d after the last page, want 1", len(replies.got))
	}
	if string(m.input) != draft || len(m.queue) != 1 || m.queue[0] != "held" {
		t.Fatalf("draft %q queue %v changed by the questionnaire", string(m.input), m.queue)
	}
	// Later keys (now reaching an emptied composer) and a late close for
	// the old generation deliver nothing more.
	m.input = nil
	generation := m.ask.generation
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	askKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	m.Update(askClosedMsg{generation: generation - 1})
	if len(replies.got) != 1 {
		t.Fatalf("replies = %d, want exactly one", len(replies.got))
	}
}

// The coordinator route (an "ask" turn event) opens the questionnaire with
// its full request and delivers one answer on the reply channel.
func TestAskQuestionnaireThroughAskEvent(t *testing.T) {
	m := toolItemTestUI(t, 100)
	startCall(m, "call_q", "ask_user", `{"questions":[{"question":"Which database?"},{"question":"Which port?"}]}`)
	reply := make(chan tools.AskAnswer, 1)
	m.Update(agent.TurnEvent{RunID: m.runID, Kind: "ask", Ask: &agent.AskInteraction{
		ID: "ask_1_1", CallID: "call_q", Reply: reply,
		Request: tools.AskRequest{Questions: []tools.AskQuestion{{Question: "Which database?"}, {Question: "Which port?"}}},
	}})
	if !m.ask.pending || !m.ask.questionnaire || len(m.ask.pages) != 2 {
		t.Fatalf("ask state %+v", m.ask)
	}
	if status, _ := askRecordStatus(m, "call_q"); status != "awaiting answer" {
		t.Fatalf("ask item status %q, want awaiting answer", status)
	}
	question := ""
	for _, e := range m.entries {
		if strings.HasPrefix(e.content, "Question from the model:") {
			question = e.content
		}
	}
	if question != "Question from the model:\n1. Which database?\n2. Which port?" {
		t.Fatalf("question entry = %q", question)
	}
	askType(m, "pg")
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	askKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	select {
	case got := <-reply:
		if len(got.Answers) != 2 || got.Answers[0] != (tools.AskQuestionAnswer{Text: "pg"}) || !got.Answers[1].Skipped {
			t.Fatalf("reply = %+v", got)
		}
	default:
		t.Fatal("no reply delivered")
	}
}

func askRecordStatus(m *ui, callID string) (string, bool) {
	for _, r := range m.toolRecords {
		if r.CallID == callID {
			return r.Status, true
		}
	}
	return "", false
}

// The single-question form keeps its look and behavior: no progress marker,
// the original hint, ←/→ inert, and a plain {Text} answer.
func TestAskSingleFormUnchanged(t *testing.T) {
	m, replies := openTestAsk(t, 100, tools.AskRequest{Question: "What should the audit focus on?", Options: []string{"Comprehensive", "Security only"}})
	if m.ask.questionnaire || m.ask.multiPage() {
		t.Fatal("single form opened as a questionnaire")
	}
	view := askViewText(m)
	if !strings.Contains(view, askHint) || strings.Contains(view, " of 1") || strings.Contains(view, "Recommended") || strings.Contains(view, "←/→") {
		t.Fatalf("single-form view changed:\n%s", view)
	}
	if !strings.Contains(view, "> 1. Comprehensive") {
		t.Fatalf("single form does not start on the first option:\n%s", view)
	}
	if question := m.entries[len(m.entries)-1].content; question != "Question from the model:\nWhat should the audit focus on?" {
		t.Fatalf("question entry = %q", question)
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	askKey(m, tea.KeyMsg{Type: tea.KeyDown})
	askKey(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.ask.selected != 1 {
		t.Fatalf("selected %d, want ←/→ inert in the single form", m.ask.selected)
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(replies.got) != 1 || replies.got[0].Text != "Security only" || replies.got[0].Answers != nil || replies.got[0].Skipped {
		t.Fatalf("single-form reply = %+v", replies.got)
	}
	if note := askLastNote(m); note != "ask answered (choice): Security only" || m.status != "Answer sent" {
		t.Fatalf("note %q status %q", note, m.status)
	}

	m, replies = openTestAsk(t, 100, tools.AskRequest{Question: "Proceed?"})
	askKey(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if len(replies.got) != 1 || !replies.got[0].Skipped || replies.got[0].Answers != nil || m.status != "Question skipped" {
		t.Fatalf("single-form skip = %+v status %q", replies.got, m.status)
	}
}

// A one-question questionnaire shows no progress marker and no page keys.
func TestAskQuestionnaireSinglePageHasNoProgress(t *testing.T) {
	m, replies := openTestAsk(t, 100, tools.AskRequest{Questions: []tools.AskQuestion{{Question: "Which database?", Options: []tools.AskOption{{Label: "SQLite"}, {Label: "Postgres", Recommended: true}}}}})
	view := askViewText(m)
	if strings.Contains(view, "1 of 1") || !strings.Contains(view, askHint) || !strings.Contains(view, "Postgres  Recommended") {
		t.Fatalf("one-page questionnaire view:\n%s", view)
	}
	askKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(replies.got) != 1 || len(replies.got[0].Answers) != 1 || replies.got[0].Answers[0] != (tools.AskQuestionAnswer{Text: "Postgres", Choice: true}) {
		t.Fatalf("reply = %+v", replies.got)
	}
}

func TestAskQuestionnaireNarrowTerminal(t *testing.T) {
	long := "A deliberately long option label that cannot fit on one narrow row"
	description := "Keeps every existing behavior and needs no migration step at all"
	req := tools.AskRequest{Questions: []tools.AskQuestion{
		{Question: "How should the change land in the repository?", Options: []tools.AskOption{
			{Label: "Short"},
			{Label: long, Description: description, Recommended: true},
		}},
		{Question: "Second?"},
		{Question: "Third?"},
		{Question: "Fourth?"},
	}}
	m, _ := openTestAsk(t, 40, req)
	view := askViewText(m)
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("view has %d rows, want %d", len(lines), m.height)
	}
	var box strings.Builder
	for _, line := range lines {
		if w := runewidth.StringWidth(line); w > 40 {
			t.Fatalf("row wider than the terminal (%d): %q", w, line)
		}
		if l, r := strings.Index(line, "│ "), strings.LastIndex(line, " │"); l >= 0 && r > l {
			box.WriteString(line[l+len("│ ") : r])
		}
	}
	for _, want := range []string{"Model question · 1 of 4", "Recommended", "Ctrl+S skip", "Esc cancel run", "←/→ question", "Enter answer", "╰"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow view lacks %q:\n%s", want, view)
		}
	}
	// Label and description wrap rather than clip: all their text is shown.
	squash := func(s string) string { return strings.Join(strings.Fields(s), "") }
	for _, want := range []string{long, description} {
		if !strings.Contains(squash(box.String()), squash(want)) {
			t.Fatalf("narrow view clipped %q:\n%s", want, view)
		}
	}
	// The tag never splits across rows.
	for _, line := range lines {
		if strings.Contains(line, "Recommen") && !strings.Contains(line, "Recommended") {
			t.Fatalf("tag split across rows:\n%s", view)
		}
	}
	// Paging keeps the progress visible too.
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	askKey(m, tea.KeyMsg{Type: tea.KeyRight})
	if view := askViewText(m); !strings.Contains(view, "Model question · 3 of 4") {
		t.Fatalf("narrow page 3:\n%s", view)
	}
}

// The tool item for a questionnaire lists each question with its answer or
// "skipped", live and after the session is resumed.
func TestAskQuestionnaireToolItemRendersAnswers(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	conn := providers.Connection{Provider: "Local OpenAI-compatible", Verified: true}
	m := NewUI(root, nil, nil, "local", conn, "", store, snapshot)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.working, m.runID, m.reasoningStream = true, 1, -1
	m.events = make(chan agent.TurnEvent, 64)
	m.entries = append(m.entries, entry{role: "You", content: "set up the db"})
	call := startCall(m, "call_q", "ask_user", `{"questions":[{"question":"Which database?","options":[{"label":"Postgres","recommended":true}]},{"question":"Which port?"},{"question":"Anything else?"}]}`)
	finishCall(m, call, tools.Result{Status: tools.Succeeded, Source: tools.Source{Kind: "builtin", Tool: "ask_user"},
		Content: `{"answers":[{"question":"Which database?","answer":"Postgres","choice":true},{"question":"Which port?","answer":"6000"},{"question":"Anything else?","skipped":true}]}`})
	endRun(m)

	wants := []string{"⏺ Ask(3 questions)", "⎿  Answered 2 of 3 questions", "Which database? → Postgres", "Which port? → 6000", "Anything else? → skipped"}
	check := func(label string, view string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(view, want) {
				t.Fatalf("%s view lacks %q:\n%s", label, want, view)
			}
		}
	}
	check("live", stripANSI(m.View()))

	saved, err := store.Load(m.snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed := NewUI(root, nil, nil, "local", conn, "", store, saved)
	resumed.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	check("resumed", stripANSI(resumed.View()))
}

// Result entries without their own question text take it from the call's
// arguments; a fully answered questionnaire counts every question.
func TestAskQuestionnaireSummaryFromArguments(t *testing.T) {
	record := session.ToolRecord{
		Name: "ask_user", Status: string(tools.Succeeded), SourceKind: "builtin",
		Arguments: `{"questions":[{"question":"Which database?"},{"question":"Which port?"}]}`,
		Content:   `{"answers":[{"text":"Postgres","choice":true},{"text":"6000"}]}`,
	}
	summary, preview, more := toolSummary(record, "…", toolPreviewLines)
	if summary != "Answered 2 questions" || more != 0 {
		t.Fatalf("summary %q more %d", summary, more)
	}
	if len(preview) != 2 || preview[0] != "Which database? → Postgres" || preview[1] != "Which port? → 6000" {
		t.Fatalf("preview = %q", preview)
	}
	// Four questions all show even though the collapsed preview is three rows.
	record.Arguments = `{"questions":[{"question":"a"},{"question":"b"},{"question":"c"},{"question":"d"}]}`
	record.Content = `{"answers":[{"skipped":true},{"text":"x"},{"text":"y"},{"text":"z"}]}`
	summary, preview, _ = toolSummary(record, "…", toolPreviewLines)
	if summary != "Answered 3 of 4 questions" || len(preview) != 4 || preview[0] != "a → skipped" {
		t.Fatalf("summary %q preview %q", summary, preview)
	}
	// An oversized result settles as limited and still lists its answers,
	// with entries echoing their (possibly shortened) question.
	record.Status = string(tools.Limited)
	record.Content = `{"answers":[{"question":"Which data…","answer":"Postgres","choice":true},{"question":"b","skipped":true}]}`
	summary, preview, _ = toolSummary(record, "…", toolPreviewLines)
	if summary != "Answered 1 of 2 questions · limited" || len(preview) != 2 || preview[0] != "Which data… → Postgres" || preview[1] != "b → skipped" {
		t.Fatalf("limited summary %q preview %q", summary, preview)
	}
	// The single-question result keeps its summary.
	single := session.ToolRecord{Name: "ask_user", Status: string(tools.Succeeded), Arguments: `{"question":"Q?"}`, Content: `{"answer":"yes"}`}
	if summary, preview, _ := toolSummary(single, "…", toolPreviewLines); summary != "Answered: yes" || preview != nil {
		t.Fatalf("single summary %q preview %q", summary, preview)
	}
}
