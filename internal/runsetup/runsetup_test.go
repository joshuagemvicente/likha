package runsetup

import (
	"context"
	"slices"
	"testing"

	"likha/internal/actions"
	"likha/internal/agent"
	"likha/internal/session"
	"likha/internal/tools"
)

func TestApplySessionWithoutStoreLeavesHooksNil(t *testing.T) {
	var options agent.RunOptions
	ApplySession(&options, nil, "abc")
	if options.SaveTask != nil || options.PlanApply != nil || options.EditJournal != nil || options.RecordChangeSet != nil {
		t.Fatal("a nil store must leave every session hook nil")
	}
}

func TestApplySessionPersistsPlan(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(t.TempDir(), root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	snapshot, err := store.Create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	var options agent.RunOptions
	ApplySession(&options, store, snapshot.ID)
	if options.SaveTask == nil || options.PlanApply == nil || options.EditJournal == nil || options.RecordChangeSet == nil {
		t.Fatal("a store must wire every session hook")
	}
	steps := []tools.PlanStep{{ID: "1", Title: "read the README", Status: string(tools.PlanPending)}}
	if err := options.PlanApply(context.Background(), steps); err != nil {
		t.Fatalf("PlanApply: %v", err)
	}
	plan, ok, err := store.LoadPlan(snapshot.ID)
	if err != nil || !ok || len(plan.Steps) != 1 || plan.Steps[0].Title != "read the README" {
		t.Fatalf("LoadPlan = %+v, %v, %v; want the applied step", plan, ok, err)
	}
}

func TestApplyCommandTrustNeedsStateDir(t *testing.T) {
	var options agent.RunOptions
	ApplyCommandTrust(&options, "", t.TempDir())
	if options.CommandTrusted != nil || options.TrustChecks != nil {
		t.Fatal("an empty state directory must leave trust hooks nil")
	}
	ApplyCommandTrust(&options, t.TempDir(), t.TempDir())
	if options.CommandTrusted == nil || options.TrustChecks == nil {
		t.Fatal("a state directory must wire both trust hooks")
	}
	if options.CommandTrusted("test", "fingerprint") {
		t.Fatal("a fresh state directory trusts no check")
	}
}

func TestSkillCatalogEmpty(t *testing.T) {
	advert, load := SkillCatalog(t.TempDir())
	if advert != "" {
		t.Fatalf("advert = %q, want empty for no skills", advert)
	}
	if _, err := load(context.Background(), "missing"); err == nil {
		t.Fatal("loading an undiscovered skill must fail")
	}
}

func TestProfileInputsBuiltinsOnly(t *testing.T) {
	run := ProfileInputs(t.TempDir(), nil)
	if !slices.Equal(run.Agents, []string{"explore", "review"}) {
		t.Fatalf("Agents = %v, want explore and review", run.Agents)
	}
	if len(run.Profiles) != 0 || len(run.Skipped) != 0 {
		t.Fatalf("Profiles = %v, Skipped = %v; want none", run.Profiles, run.Skipped)
	}
}

func TestApplySessionRecordsChangeSet(t *testing.T) {
	store, err := session.Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	snapshot, err := store.Create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	var options agent.RunOptions
	ApplySession(&options, store, snapshot.ID)
	set := actions.ChangeSet{Files: []actions.ChangeSetFile{{Path: "a.txt"}}}
	if err := options.RecordChangeSet("call_1", set); err != nil {
		t.Fatalf("RecordChangeSet: %v", err)
	}
	record, ok, err := store.LatestChangeSet(snapshot.ID)
	if err != nil || !ok || record.ToolCallID != "call_1" {
		t.Fatalf("LatestChangeSet = %+v, %v, %v; want the recorded set", record, ok, err)
	}
}
