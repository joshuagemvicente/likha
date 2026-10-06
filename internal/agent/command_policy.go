package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"likha/internal/cmdpolicy"
)

// AutoCommandTimeout bounds a command that ran without a prompt. Prompted
// commands keep the user's explicit decision and have no timeout.
const AutoCommandTimeout = 10 * time.Minute

// CommandApprovalPrefix opens the second line of an auto-approved
// run_command result; the transcript renders it as a label.
const CommandApprovalPrefix = "Approval: "

// authorizeCommand applies specs/command-permissions to one validated
// command. A nil error means it may run; *auto names why when no prompt was
// shown. Refused commands never reach the user as a prompt.
func authorizeCommand(ctx context.Context, root, command string, options RunOptions, emit func(TurnEvent), auto *string) error {
	decision := cmdpolicy.Classify(command, root)
	switch decision.Tier {
	case cmdpolicy.Refuse:
		return fmt.Errorf("refused: this command %s. Likha never runs it; if it is really intended, the user can run it themselves in a terminal", decision.Reason)
	case cmdpolicy.ReadOnly:
		*auto = "read-only inspection"
		return nil
	case cmdpolicy.Verify:
		if options.CommandTrusted != nil && options.CommandTrusted(decision.Check, decision.Fingerprint) {
			*auto = "verification check in a trusted repository"
			return nil
		}
		// Without trust storage the prompt offers a session grant instead.
		if options.CommandGrants.Allowed(command) {
			*auto = AutoApprovalReason
			return nil
		}
	case cmdpolicy.Ask:
		if options.CommandGrants.Allowed(command) {
			*auto = AutoApprovalReason
			return nil
		}
	}

	body := "Working directory: " + root + "\nCommand:\n" + command + "\n"
	request := &ApprovalRequest{Kind: "command", Title: "Shell command"}
	switch decision.Tier {
	case cmdpolicy.AlwaysAsk:
		request.Warning = "Always asks: this command " + decision.Reason + "."
	case cmdpolicy.Verify:
		body += "\nVerification check: " + decision.Reason + "."
		if decision.Script != "" {
			body += "\nThe script runs: " + decision.Script
		}
		if options.TrustChecks != nil {
			request.Remember = RememberTrust
			body += "\n\"Trust repo checks\" runs this repository's test, lint, build, and typecheck commands without asking until one of their scripts or the Makefile changes. Trust is stored in Likha's private config, never in the repository."
		} else if options.CommandGrants != nil {
			request.Remember = RememberSession
		}
		body += "\n"
	case cmdpolicy.Ask:
		if decision.Reason != "" {
			body += "\nNote: " + decision.Reason + "."
		}
		if options.CommandGrants != nil {
			request.Remember = RememberSession
		}
	}
	if request.Remember == RememberSession {
		body += "\n\"Approve always\" runs " + cmdpolicy.ScopeFor(command).Describe() + " without asking for the rest of this session.\n"
	}
	request.Body = body + "\nApproved commands can access files outside this repository and use the network. Detached processes may outlive cancellation."

	approved, err := awaitApproval(ctx, request, emit)
	if err != nil {
		return err
	}
	if !approved {
		return errors.New("command rejected by user")
	}
	if request.Remembered {
		switch request.Remember {
		case RememberSession:
			options.CommandGrants.Allow(command)
		case RememberTrust:
			if err := options.TrustChecks(cmdpolicy.RepoChecks(root)); err != nil {
				// The user approved this run; failing to remember the
				// trust only means the next check prompts again.
				emit(TurnEvent{Kind: "notice", Text: "Could not store repository trust: " + strings.TrimSpace(err.Error()) + ". This command runs; the next check will ask again."})
			}
		}
	}
	return nil
}
