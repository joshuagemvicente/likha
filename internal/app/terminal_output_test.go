package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// overlapRecorder fails when two writes are in flight at once and keeps each
// write as one chunk, so a test can see whether a signal split a frame.
type overlapRecorder struct {
	inFlight atomic.Int32
	overlap  atomic.Bool
	mu       sync.Mutex
	chunks   [][]byte
}

func (r *overlapRecorder) Write(p []byte) (int, error) {
	if r.inFlight.Add(1) > 1 {
		r.overlap.Store(true)
	}
	defer r.inFlight.Add(-1)
	time.Sleep(50 * time.Microsecond) // widen the window a race would need
	r.mu.Lock()
	r.chunks = append(r.chunks, append([]byte(nil), p...))
	r.mu.Unlock()
	return len(p), nil
}

func TestTerminalOutputSerializesFramesAndSignals(t *testing.T) {
	recorder := &overlapRecorder{}
	out := newTerminalOutput(recorder)
	frame := bytes.Repeat([]byte("frame-row\r\n"), 200)
	signal := []byte("\a\x1b]9;Likha · repo: approval needed\a")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 25 {
				if _, err := out.Write(frame); err != nil {
					t.Error(err)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 25 {
				if _, err := out.Write(signal); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if recorder.overlap.Load() {
		t.Fatal("two writes reached the terminal at the same time")
	}
	for _, chunk := range recorder.chunks {
		if !bytes.Equal(chunk, frame) && !bytes.Equal(chunk, signal) {
			t.Fatalf("a write reached the terminal split or merged: %q", chunk)
		}
	}
	if got := len(recorder.chunks); got != 200 {
		t.Fatalf("chunks = %d, want 200", got)
	}
}

// Bubble Tea detects the terminal through a term.File assertion (Read,
// Write, Close, Fd); wrapping stdout must keep those methods, and Close must
// leave the process's file open.
func TestTerminalOutputKeepsFileMethods(t *testing.T) {
	type terminalFile interface {
		io.ReadWriteCloser
		Fd() uintptr
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := newTerminalOutput(f)
	file, ok := out.(terminalFile)
	if !ok {
		t.Fatalf("%T does not keep the term.File methods", out)
	}
	if file.Fd() != f.Fd() {
		t.Fatalf("Fd = %d, want %d", file.Fd(), f.Fd())
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("still open")); err != nil {
		t.Fatalf("write after Close: %v (Close must not close stdout)", err)
	}
	if _, ok := newTerminalOutput(&bytes.Buffer{}).(terminalFile); ok {
		t.Fatal("a non-file writer must not pretend to be a terminal")
	}
}
