package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"likha/internal/actions"
	"likha/internal/tools"
)

type recordedChangeSet struct {
	callID string
	set    actions.ChangeSet
}

func invokeWithCallID(t *testing.T, registry *tools.Registry, name, arguments string) (tools.Result, error) {
	t.Helper()
	call := toolCall(name, arguments)
	return registry.Invoke(WithTaskCallID(context.Background(), call.ID), mainScope(tools.Scope{}), call)
}

func TestAppliedEditsRecordChangeSets(t *testing.T) {
	var recorded []recordedChangeSet
	options := RunOptions{EditGrant: &EditGrant{}, RecordChangeSet: func(callID string, set actions.ChangeSet) error {
		recorded = append(recorded, recordedChangeSet{callID, set})
		return nil
	}}
	reviewer := &fakeReviewer{approve: true, remember: true}
	registry, root := initRegistry(t, options, reviewer)

	result, err := invokeWithCallID(t, registry, "edit_file", editFileArgs(t, "a.txt", "one\n"))
	if err != nil || len(result.Warnings) != 0 {
		t.Fatalf("edit_file: %+v %v", result, err)
	}
	// Approve always was chosen above: the next edit runs without a review
	// and must still be recorded.
	result, err = invokeWithCallID(t, registry, "edit", grantEditArgs(t,
		actions.EditOperation{Kind: "replace", Path: "a.txt", OldText: "one", NewText: "uno"},
		actions.EditOperation{Kind: "create", Path: filepath.Join("pkg", "b.txt"), Content: "two\n"}))
	if err != nil || len(result.Warnings) != 0 {
		t.Fatalf("edit: %+v %v", result, err)
	}
	if len(reviewer.requests) != 1 {
		t.Fatalf("reviews = %d, want 1 (the second edit ran under Approve always)", len(reviewer.requests))
	}
	if len(recorded) != 2 {
		t.Fatalf("recorded %d change sets, want 2", len(recorded))
	}
	if recorded[0].callID != "call-edit_file" || recorded[1].callID != "call-edit" {
		t.Fatalf("change set call IDs = %q, %q", recorded[0].callID, recorded[1].callID)
	}
	if got := recorded[0].set.Paths(); !reflect.DeepEqual(got, []string{"a.txt"}) || recorded[0].set.Files[0].Existed {
		t.Fatalf("edit_file change set = %+v", recorded[0].set)
	}
	second := recorded[1].set
	if got := second.Paths(); !reflect.DeepEqual(got, []string{"a.txt", filepath.Join("pkg", "b.txt")}) ||
		second.Files[0].OldText != "one\n" || !reflect.DeepEqual(second.CreatedDirs, []string{"pkg"}) {
		t.Fatalf("edit change set = %+v", second)
	}

	// The recorded set is enough to revert the second edit.
	proposal, changed, err := actions.PrepareUndo(root, second)
	if err != nil || len(changed) != 0 {
		t.Fatalf("PrepareUndo: %v %v", changed, err)
	}
	if _, err := proposal.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != "one\n" {
		t.Fatalf("a.txt after undo = %q", data)
	}
}

func TestRejectedEditRecordsNoChangeSet(t *testing.T) {
	recorded := 0
	options := RunOptions{RecordChangeSet: func(string, actions.ChangeSet) error {
		recorded++
		return nil
	}}
	registry, _ := initRegistry(t, options, &fakeReviewer{approve: false})
	if _, err := invokeWithCallID(t, registry, "edit_file", editFileArgs(t, "a.txt", "one\n")); err == nil {
		t.Fatal("declined edit succeeded")
	}
	if recorded != 0 {
		t.Fatalf("a declined edit recorded %d change sets", recorded)
	}
}

func TestChangeSetRecordingFailureKeepsEditApplied(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		warning string
	}{
		{"storage failure", errors.New("database is locked"), "not undoable: undo history could not be saved"},
		{"too large", fmt.Errorf("%w: 70 MiB", ErrChangeSetTooLarge), "not undoable: too large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := RunOptions{RecordChangeSet: func(string, actions.ChangeSet) error { return tc.err }}
			registry, root := initRegistry(t, options, &fakeReviewer{approve: true})
			result, err := invokeWithCallID(t, registry, "edit_file", editFileArgs(t, "a.txt", "one\n"))
			if err != nil {
				t.Fatalf("a recording failure failed the applied edit: %v", err)
			}
			if result.Status != tools.Succeeded || !reflect.DeepEqual(result.Warnings, []string{tc.warning}) {
				t.Fatalf("result = %+v", result)
			}
			if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != "one\n" {
				t.Fatalf("edit not applied: %q", data)
			}
		})
	}
}
