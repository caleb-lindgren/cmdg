package input

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDuration(t *testing.T) {
	for _, test := range []struct {
		i time.Duration
		o unix.Timeval
	}{
		{i: time.Second, o: unix.Timeval{Sec: 1, Usec: 0}},
		{i: time.Millisecond, o: unix.Timeval{Sec: 0, Usec: 1000}},
	} {
		if got := *duration2Timeval(test.i); got != test.o {
			t.Errorf("got %v want %v", got, test.o)
		}
	}
}

func TestMaxTimeoutSpace(t *testing.T) {
	now := time.Now()
	timeout := time.Duration(100)
	deadline := now.Add(time.Duration(500))
	if got, want := maxTimeoutNow(now, deadline, timeout), time.Duration(100); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
func TestMaxTimeoutNoSpace(t *testing.T) {
	now := time.Now()
	timeout := time.Duration(700)
	deadline := now.Add(time.Duration(500))
	if got, want := maxTimeoutNow(now, deadline, timeout), time.Duration(500); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
func TestMaxTimeoutNegative(t *testing.T) {
	now := time.Now()
	timeout := time.Duration(100)
	deadline := now.Add(-time.Duration(100))
	if got, want := maxTimeoutNow(now, deadline, timeout), time.Duration(0); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestReadFocusKeys checks that the focus reports a terminal sends are read
// as whole keys, and not mistaken for Escape followed by letters.
func TestReadFocusKeys(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	// readByte waits on the descriptor it is given, but reads stdin.
	stdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = stdin }()
	if _, err := w.WriteString(FocusOut + FocusIn + "j"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{FocusOut, FocusIn, "j"} {
		got, err := readKey(int(r.Fd()))
		if err != nil {
			t.Fatalf("reading %q: %v", want, err)
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// TestFocused checks that the window is taken to have the focus until the
// terminal says otherwise, and that Focus is told of changes without the
// input loop waiting for them to be read.
func TestFocused(t *testing.T) {
	i := New()
	if !i.Focused() {
		t.Errorf("focused before any report: false, want true")
	}
	i.setFocused(false)
	i.setFocused(true)
	i.setFocused(false)
	if i.Focused() {
		t.Errorf("focused after FocusOut: true, want false")
	}
	select {
	case <-i.Focus():
	default:
		t.Fatalf("Focus not told of the change")
	}
	select {
	case <-i.Focus():
		t.Errorf("Focus told twice; changes not read are one")
	default:
	}
}
