package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// throttleFile is the single opaque file Lisa writes for update checks; it
// holds the Unix-nano timestamp of the last completed check.
const throttleFile = "update_check.json"

// interval bounds the network check to at most once per day per machine.
const interval = 24 * time.Hour

type state struct {
	CheckedNS int64 `json:"checked_ns"`
}

// Prepared reports whether an update check should run now, and hands back a
// mark function that persists the completed check. The bool is false when the
// check was throttled (ran within interval) or opted out via
// LISA_UPDATE_CHECK=0; both are non-events, not errors.
func Prepared(stateDir string) (shouldCheck bool, mark func() error, err error) {
	if os.Getenv("LISA_UPDATE_CHECK") == "0" {
		return false, nil, nil
	}
	should, err := throttleAllows(stateDir)
	if err != nil {
		return false, nil, err
	}
	if !should {
		return false, nil, nil
	}
	save := func() error { return writeState(stateDir, time.Now().UnixNano()) }
	return true, save, nil
}

// throttleAllows reads the throttle file; missing or unreadable means "check
// now" — a corrupt timestamp must not permanently disable the check.
func throttleAllows(stateDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, throttleFile))
	if err != nil {
		return true, nil
	}
	var st state
	if json.Unmarshal(data, &st) != nil {
		return true, nil
	}
	return time.Since(time.Unix(0, st.CheckedNS)) >= interval, nil
}

// writeState atomically persists the given timestamp: write a temp file in the
// state dir, then rename over the target so a crash cannot leave a partial
// file behind.
func writeState(stateDir string, now int64) error {
	if mkerr := os.MkdirAll(stateDir, 0o700); mkerr != nil {
		return fmt.Errorf("creating state directory: %w", mkerr)
	}
	data, err := json.Marshal(state{CheckedNS: now})
	if err != nil {
		return fmt.Errorf("encoding %s: %w", throttleFile, err)
	}
	tmp, err := os.CreateTemp(stateDir, throttleFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary %s: %w", throttleFile, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temporary %s: %w", throttleFile, err)
	}
	if err = tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting permissions on temporary %s: %w", throttleFile, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary %s: %w", throttleFile, err)
	}
	target := filepath.Join(stateDir, throttleFile)
	if err = os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("persisting %s: %w", throttleFile, err)
	}
	return nil
}
