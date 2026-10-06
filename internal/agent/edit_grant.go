package agent

import (
	"context"
	"errors"
)

// autoEditLabel opens the result of an edit applied under the session's
// Approve always grant, so the model and the stored record carry it; the
// transcript renders it as a label, like an auto-approved command.
const autoEditLabel = CommandApprovalPrefix + "auto-approved (" + AutoApprovalReason + ")\n"

// authorizeEdit applies specs/approve-always to one prepared edit review. It
// reports auto == true when the session's edit grant let the edit through
// without a review. A warning-carrying edit and an /init proposal always ask
// and never offer Approve always; cancellation or a decline never grants.
func authorizeEdit(ctx context.Context, request *ApprovalRequest, options RunOptions, emit func(TurnEvent)) (auto bool, err error) {
	alwaysAsk := request.Warning != "" || options.InitMode
	if !alwaysAsk && options.EditGrant.Allowed() {
		return true, nil
	}
	if !alwaysAsk && options.EditGrant != nil {
		request.Remember = RememberEdits
	}
	approved, err := awaitApproval(ctx, request, emit)
	if err != nil {
		return false, err
	}
	if !approved {
		return false, errors.New("edit rejected by user")
	}
	if request.Remembered && request.Remember == RememberEdits {
		options.EditGrant.Allow()
	}
	return false, nil
}
