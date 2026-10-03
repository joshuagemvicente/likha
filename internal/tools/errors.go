package tools

// CallError is a safe, attributed dispatch error. Invoke also returns a Result
// on error; callers must still reconcile that result with the original call ID.
// Cause is available for errors.Is/errors.As, but is never model-visible.
type CallError struct {
	Code    string
	Tool    string
	Status  Status
	Message string
	Cause   error
}

func (e *CallError) Error() string { return e.Message }
func (e *CallError) Unwrap() error { return e.Cause }

func callFailure(name string, source Source, status Status, code, message string, cause error) (Result, error) {
	err := &CallError{Code: code, Tool: name, Status: status, Message: message, Cause: cause}
	return Result{Status: status, Source: source, Content: code + ": " + message}, err
}
