// Package headless runs one Likha prompt without a terminal UI
// (specs/headless-print, FR-44). Nothing prompts: every action that would
// open a review or a question is refused and reported, never approved.
package headless

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"likha/internal/agent"
	"likha/internal/hooks"
	"likha/internal/model"
	"likha/internal/redact"
	"likha/internal/repository"
	"likha/internal/runsetup"
	"likha/internal/session"
	"likha/internal/tooloutput"
	"likha/internal/tools"
)

// Exit codes (spec drafting default D2).
const (
	ExitCompleted   = 0
	ExitFailed      = 1
	ExitUsage       = 2
	ExitInterrupted = 130
)

// NoReviewerReason is the refusal reason every review gets in a headless run.
const NoReviewerReason = "no reviewer is present in headless mode"

// ErrInterrupted is the cancellation cause a caller sets when SIGINT or
// SIGTERM stops the run; Run then reports cancelled and exits 130.
var ErrInterrupted = errors.New("interrupted by signal")

// errTimeLimit is the cancellation cause when --max-time elapses.
var errTimeLimit = errors.New("time limit reached")

// Run statuses reported in the summary and the result event.
const (
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusCancelled = "cancelled"
	statusTimeLimit = "time_limit"
)

// Config is one headless run. Client, Repo and Store must be ready: the
// caller has resolved the provider, checked the connection and created or
// loaded Snapshot.
type Config struct {
	Prompt   string
	Root     string
	Repo     *repository.Repository
	Client   *model.Client
	Store    *session.Store
	Snapshot session.Snapshot
	StateDir string
	// Provider is the canonical provider ID used for pricing; Display is
	// its human name for messages.
	Provider, Display string
	// Effort is the effort level sent, or "" for the provider default.
	Effort string
	// JSON selects JSON Lines on stdout instead of text.
	JSON bool
	// MaxTime bounds the whole run; zero means no bound.
	MaxTime time.Duration
	// Secrets are removed from every message Likha writes.
	Secrets []string
	// Notices are startup notes reported before the run starts.
	Notices []string
}

// Run executes one run and returns its exit code. ctx cancellation with
// cause ErrInterrupted reports the run as interrupted.
func Run(ctx context.Context, cfg Config, stdout, stderr io.Writer) int {
	if cfg.MaxTime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, cfg.MaxTime, errTimeLimit)
		defer cancel()
	}
	out := newRenderer(cfg, stdout, stderr)
	options, notices := runOptions(cfg)
	resumed := len(cfg.Snapshot.History) > 0
	rec := newRecorder(cfg)
	out.start(startInfo{
		SessionID: cfg.Snapshot.ID, Repository: cfg.Root, Provider: cfg.Display,
		Model: cfg.Client.Model(), Effort: effortName(cfg.Effort),
	})
	runner, hookNotice := loadHooks(cfg.StateDir)
	options.Hooks = runner
	if hookNotice != "" {
		notices = append(notices, hookNotice)
	}
	for _, notice := range append(append([]string(nil), cfg.Notices...), notices...) {
		out.event(agent.TurnEvent{Kind: "notice", Text: notice})
		rec.observe(agent.TurnEvent{Kind: "notice", Text: notice})
	}

	// Parallel read-only tool groups and background hooks emit from several
	// goroutines.
	var mu sync.Mutex
	deliver := func(ev agent.TurnEvent) {
		mu.Lock()
		defer mu.Unlock()
		rec.observe(ev)
		out.event(ev)
	}
	hookRun := &hookSet{runner: runner, sessionID: cfg.Snapshot.ID, root: cfg.Root, warn: func(text string) {
		deliver(agent.TurnEvent{Kind: "notice", Text: text})
	}}
	hookRun.sessionStart(resumed)
	if block := hookRun.promptBlocked(ctx, cfg.Prompt); block != nil {
		// Nothing was sent or saved; the prompt is the user's to change.
		hookRun.wait()
		return finishBlocked(cfg, out, stderr, block)
	}
	agent.RunTurnWithOptions(ctx, cfg.Client, cfg.Repo, cfg.Root, cfg.Snapshot.History, cfg.Prompt, nil, nil, options, func(ev agent.TurnEvent) {
		if hookRun.turnEvent(ev) {
			return
		}
		deliver(ev)
	})

	result := rec.result(ctx)
	hookRun.runEnd(result)
	hookRun.wait()
	if err := save(cfg, rec, resumed); err != nil {
		result.markSaveFailed(err)
	}
	if err := out.finish(result); err != nil {
		fmt.Fprintf(stderr, "likha: write output: %v\n", err)
		return ExitFailed
	}
	return result.exitCode()
}

// finishBlocked reports a run whose prompt a prompt_submit hook blocked: no
// request was sent and nothing was saved, and the run exits 1 (failed).
func finishBlocked(cfg Config, out renderer, stderr io.Writer, block *hooks.BlockError) int {
	result := runResult{SessionID: cfg.Snapshot.ID, Status: statusFailed, Err: cfg.clean(block.PromptMessage()), Totals: totals{CostKnown: true}}
	if err := out.finish(result); err != nil {
		fmt.Fprintf(stderr, "likha: write output: %v\n", err)
	}
	return ExitFailed
}

// runOptions builds the agent options for a run nobody can review: no ask
// broker, no web consent, no steering, no grants, and NoReviewer set. It
// returns notices for profiles held out of the run.
func runOptions(cfg Config) (agent.RunOptions, []string) {
	options := agent.RunOptions{
		SessionID:  cfg.Snapshot.ID,
		Provider:   cfg.Provider,
		NoReviewer: NoReviewerReason,
	}
	var notices []string
	if cfg.StateDir != "" && cfg.Snapshot.ID != "" {
		outputs, err := tooloutput.Open(cfg.StateDir, cfg.Snapshot.ID)
		if err != nil {
			notices = append(notices, "Private tool output unavailable: "+err.Error())
		} else {
			options.Outputs = outputs
		}
	}
	if cfg.Store != nil && cfg.Snapshot.ID != "" {
		options.TasksEnabled = cfg.Client != nil && cfg.Repo != nil
		runsetup.ApplySession(&options, cfg.Store, cfg.Snapshot.ID)
	}
	options.SkillAdvert, options.SkillLoad = runsetup.SkillCatalog(cfg.StateDir)
	profiles := runsetup.ProfileInputs(cfg.StateDir, cfg.Client)
	options.TaskAgents, options.TaskProfiles = profiles.Agents, profiles.Profiles
	if len(profiles.Catalog.Errors) > 0 {
		notices = append(notices, "Profile catalog: "+strings.Join(profiles.Catalog.Errors, "; "))
	}
	for _, skip := range profiles.Skipped {
		if skip.Err != nil {
			notices = append(notices, "Profile "+skip.Profile.Name+" refused: "+skip.Err.Error())
			continue
		}
		notices = append(notices, fmt.Sprintf("Profile %s requires model %s; the active provider is serving %s, so it is not advertised this run.", skip.Profile.Name, skip.Profile.Model, skip.Serving))
	}
	runsetup.ApplyCommandTrust(&options, cfg.StateDir, cfg.Root)
	// A headless run can never record trust; the hook only fires after a
	// reviewed approval, which cannot happen here.
	options.TrustChecks = nil
	return options, notices
}

func effortName(level string) string {
	if level == "" {
		return "default"
	}
	return level
}

// bound cuts text to at most limit bytes on a character boundary.
func bound(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// clean removes credentials and keeps text valid UTF-8.
func (c Config) clean(text string) string {
	return redact.Secrets(strings.ToValidUTF8(text, "�"), c.Secrets...)
}

// isRefusal reports whether a tool result was refused rather than run.
func isRefusal(result *tools.Result) bool {
	return result != nil && result.Status == tools.Refused
}
