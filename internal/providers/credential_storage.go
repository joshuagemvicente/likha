package providers

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// One lock covers the whole credential file, not just one registration:
// otherwise a refresh could overwrite a simultaneous update to another
// provider. The on-disk lock is never removed or replaced.
var credentialLocks sync.Map // absolute state directory -> chan struct{}

func lockCredentials(ctx context.Context, stateDir string) (func(), error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, fmt.Errorf("private credential state directory is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("creating private credential directory: %w", err)
	}
	if err := os.Chmod(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("protecting private credential directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(stateDir)
	if err != nil {
		return nil, fmt.Errorf("resolving private credential directory: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, err
	}
	value, _ := credentialLocks.LoadOrStore(canonical, make(chan struct{}, 1))
	gate := value.(chan struct{})
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	releaseGate := func() { <-gate }
	f, err := openPrivateFile(filepath.Join(canonical, ".providers.lock"), true)
	if err != nil {
		releaseGate()
		return nil, fmt.Errorf("opening credential lock: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			releaseGate()
			return nil, err
		}
		locked, err := tryCredentialFileLock(f)
		if err != nil {
			f.Close()
			releaseGate()
			return nil, fmt.Errorf("locking stored credentials: %w", err)
		}
		if locked {
			return func() {
				// Closing this dedicated descriptor releases flock as well.
				f.Close()
				releaseGate()
			}, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			releaseGate()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func readPrivateFile(path string) ([]byte, error) {
	f, err := openPrivateFile(path, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// writePrivateFile requires the credential lock. It never truncates the
// destination, so a reader observes either complete old or complete new data.
func writePrivateFile(path string, data []byte) error {
	if f, err := openPrivateFile(path, false); err == nil {
		if err := f.Close(); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if n, err := f.Write(data); err != nil {
		return err
	} else if n != len(data) {
		return io.ErrShortWrite
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
