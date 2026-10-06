package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"likha/internal/actions"
	"likha/internal/tools"
)

func grantEditArgs(t *testing.T, ops ...actions.EditOperation) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"operations": ops})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEditApproveAlwaysGrantsLaterEdits(t *testing.T) {
	grant := &EditGrant{}
	reviewer := &fakeReviewer{approve: true, remember: true}
	registry, root := initRegistry(t, RunOptions{EditGrant: grant}, reviewer)

	first, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "a.txt", "one\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewer.requests) != 1 || reviewer.requests[0].Remember != RememberEdits {
		t.Fatalf("first edit review = %+v", reviewer.requests)
	}
	if !grant.Allowed() {
		t.Fatal("a remembered edit approval did not set the grant")
	}
	if first.Diff != "" || strings.Contains(first.Content, "auto-approved") {
		t.Fatalf("a reviewed edit was labelled auto-approved: %+v", first)
	}

	later, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "b.txt", "two\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("granted edit_file asked again: %d reviews", len(reviewer.requests))
	}
	if !strings.HasPrefix(later.Content, "Approval: auto-approved (approved always for this session)\n") || !strings.Contains(later.Diff, "+two") {
		t.Fatalf("granted edit_file result = %+v", later)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(content) != "two\n" {
		t.Fatalf("granted edit_file wrote %q", content)
	}

	batch, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit", grantEditArgs(t, actions.EditOperation{Kind: "replace", Path: "a.txt", OldText: "one\n", NewText: "uno\n"})))
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("granted edit asked again: %d reviews", len(reviewer.requests))
	}
	if !strings.HasPrefix(batch.Content, autoEditLabel) || !strings.HasPrefix(batch.Diff, "Affected files:\na.txt\n") || !strings.Contains(batch.Diff, "+uno") {
		t.Fatalf("granted edit result = %+v", batch)
	}
}

func TestEditGrantStillValidatesProposals(t *testing.T) {
	grant := &EditGrant{}
	grant.Allow()
	reviewer := &fakeReviewer{approve: true}
	registry, root := initRegistry(t, RunOptions{EditGrant: grant}, reviewer)
	_, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit", grantEditArgs(t, actions.EditOperation{Kind: "replace", Path: "missing.txt", OldText: "x", NewText: "y"})))
	if err == nil || len(reviewer.requests) != 0 {
		t.Fatalf("invalid edit under the grant: err=%v reviews=%d", err, len(reviewer.requests))
	}
	if _, statErr := os.Stat(filepath.Join(root, "missing.txt")); !os.IsNotExist(statErr) {
		t.Fatal("an invalid edit under the grant wrote a file")
	}
}

func TestEditGrantNeverSkipsWarningsOrInit(t *testing.T) {
	grant := &EditGrant{}
	grant.Allow()

	reviewer := &fakeReviewer{approve: false}
	registry, _ := initRegistry(t, RunOptions{EditGrant: grant}, reviewer)
	registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "AGENTS.md", strings.Repeat("a\n", 16385))))
	if len(reviewer.requests) != 1 || reviewer.requests[0].Warning == "" || reviewer.requests[0].Remember != "" {
		t.Fatalf("oversized AGENTS.md under the grant = %+v", reviewer.requests)
	}

	reviewer = &fakeReviewer{approve: false}
	registry, _ = initRegistry(t, RunOptions{EditGrant: grant, InitMode: true}, reviewer)
	registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "AGENTS.md", "# Agents\n")))
	if len(reviewer.requests) != 1 || reviewer.requests[0].Remember != "" {
		t.Fatalf("/init proposal under the grant = %+v", reviewer.requests)
	}
}

func TestEditGrantNeverSetByDeclineOrCancel(t *testing.T) {
	grant := &EditGrant{}
	reviewer := &fakeReviewer{approve: false, remember: true}
	registry, _ := initRegistry(t, RunOptions{EditGrant: grant}, reviewer)
	registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "a.txt", "x\n")))
	if grant.Allowed() {
		t.Fatal("a declined review set the grant")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo, root := initRepo(t)
	registry, _ = newToolRegistry(repo, root, nil, RunOptions{EditGrant: grant, EditJournal: func(*actions.EditProposal) error { return nil }}, func(ev TurnEvent) {
		if ev.Kind == "approval" {
			ev.Approval.Remembered = true
			cancel() // no reply: the run ends while the review is open
		}
	})
	if _, err := registry.Invoke(ctx, mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "a.txt", "x\n"))); err == nil {
		t.Fatal("a cancelled review applied the edit")
	}
	if grant.Allowed() {
		t.Fatal("a cancelled review set the grant")
	}
}

func TestEditWithoutGrantOffersNoApproveAlways(t *testing.T) {
	reviewer := &fakeReviewer{approve: true, remember: true}
	registry, _ := initRegistry(t, RunOptions{}, reviewer)
	registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "a.txt", "x\n")))
	if len(reviewer.requests) != 1 || reviewer.requests[0].Remember != "" {
		t.Fatalf("edit review without a grant handle = %+v", reviewer.requests)
	}
}

func TestResultDiffNeverReachesModelContent(t *testing.T) {
	plain := tools.Result{Status: tools.Succeeded, Content: "Applied edit to a.txt"}
	withDiff := plain
	withDiff.Diff = "--- a/a.txt\n+++ b/a.txt\n"
	if plain.ModelContent() != withDiff.ModelContent() {
		t.Fatalf("Diff leaked into model content: %q", withDiff.ModelContent())
	}
	limited := tools.Result{Status: tools.Limited, Content: "x", Warnings: []string{"w"}, Diff: "secret-diff"}
	if strings.Contains(limited.ModelContent(), "secret-diff") {
		t.Fatal("Diff leaked into the JSON envelope")
	}
	if bound := tools.BoundResult(limited); bound.Diff != "secret-diff" {
		t.Fatal("BoundResult dropped Diff")
	}
	if finished := finishToolResult(toolCall("edit", "{}"), withDiff, nil, nil); finished.Diff != withDiff.Diff {
		t.Fatal("finishToolResult dropped Diff")
	}
}
