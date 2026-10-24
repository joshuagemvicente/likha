package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"likha/internal/actions"
	"likha/internal/hooks"
	"likha/internal/repository"
	"likha/internal/tools"
)

// A prompt_submit hook interrupted by Esc never said yes: the steering
// prompt it was screening must not reach the history or the model.
func TestSteeringPromptCancelledDuringHookIsNotDelivered(t *testing.T) {
	root := t.TempDir()
	started := filepath.Join(t.TempDir(), "started")
	guard := hookScript(t, `cat >/dev/null; touch `+started+`; sleep 30`)
	options := RunOptions{SessionID: "s1", Hooks: hookRunner(hooks.Hook{Event: hooks.PromptSubmit, Name: "guard", Command: guard, Timeout: time.Minute})}
	client, script := scriptedClient(t, func(int) string { return doneFrame })
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	steer := make(chan string, 1)
	steer <- "sneaky steering prompt"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	var events []TurnEvent
	RunTurnWithOptions(ctx, client, repo, root, nil, "go", nil, steer, options, func(ev TurnEvent) { events = append(events, ev) })
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("the prompt_submit hook never ran: %v", err)
	}
	for _, ev := range events {
		if ev.Kind == "steer" {
			t.Fatalf("a steering prompt was delivered although its hook was cancelled: %+v", ev)
		}
		for _, message := range ev.History {
			if strings.Contains(message.Content, "sneaky") {
				t.Fatalf("%s event history holds the unscreened prompt", ev.Kind)
			}
		}
	}
	for _, body := range script.requests() {
		if strings.Contains(body, "sneaky") {
			t.Fatal("an unscreened steering prompt reached the model")
		}
	}
	last := events[len(events)-1]
	if last.Kind != "error" || !errors.Is(last.Err, context.Canceled) {
		t.Fatalf("last event = %s (%v), want the cancelled run's error", last.Kind, last.Err)
	}
}

func TestEditJournalFailureKeepsCause(t *testing.T) {
	cause := errors.New("session store is read-only")
	options := RunOptions{EditJournal: func(*actions.EditProposal) error { return cause }}
	registry, root := initRegistry(t, options, &fakeReviewer{approve: true})
	_, err := invokeWithCallID(t, registry, "edit", grantEditArgs(t, actions.EditOperation{Kind: "create", Path: "a.txt", Content: "one\n"}))
	if !errors.Is(err, cause) {
		t.Fatalf("edit error = %v, want it to wrap the journal cause", err)
	}
	if message := modelError(err); !strings.Contains(message, "no file changes applied") || !strings.Contains(message, cause.Error()) {
		t.Fatalf("model-visible error = %q", message)
	}
	if _, statErr := os.Stat(filepath.Join(root, "a.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("edit applied without a journal: %v", statErr)
	}
}

func TestChangeSetRecordingFailureTellsUserTheCause(t *testing.T) {
	options := RunOptions{RecordChangeSet: func(string, actions.ChangeSet) error {
		return errors.New("database is locked")
	}}
	reviewer := &fakeReviewer{approve: true}
	registry, _ := initRegistry(t, options, reviewer)
	result, err := invokeWithCallID(t, registry, "edit_file", editFileArgs(t, "a.txt", "one\n"))
	if err != nil || result.Status != tools.Succeeded {
		t.Fatalf("edit_file: %+v %v", result, err)
	}
	for _, notice := range reviewer.notices {
		if strings.Contains(notice, "database is locked") && strings.Contains(notice, "a.txt") {
			return
		}
	}
	t.Fatalf("notices = %q, want one naming the file and the storage cause", reviewer.notices)
}

func TestCommandBackgroundChildIsReportedNotFailed(t *testing.T) {
	root := commandRepo(t)
	registry, _ := newToolRegistry(nil, root, nil, RunOptions{}, (&fakeReviewer{approve: true}).emit)
	result, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("run_command", `{"command":"echo hi; sleep 30 &"}`))
	if err != nil {
		t.Fatalf("run_command: %v (%+v)", err, result)
	}
	if !strings.Contains(result.Content, "Exit status: 0\n") || !strings.Contains(result.Content, "hi\n") {
		t.Fatalf("content = %q", result.Content)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "background process") {
			return
		}
	}
	t.Fatalf("warnings = %q, want a note that background output was cut off", result.Warnings)
}
