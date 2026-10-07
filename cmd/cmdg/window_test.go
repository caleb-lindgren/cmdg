package main

import (
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
)

func TestPassedOnFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("log", "/dev/null", "")
	fs.Bool("sign", false, "")
	fs.String("gpg", "gpg", "")
	fs.Bool("configure", false, "")
	fs.String("terminal", "st -e", "")
	fs.String("update_sender", "", "")
	fs.String("read", "", "")
	fs.String("reply", "", "")
	fs.Bool("continue_draft", false, "")
	fs.String("window_file", "", "")
	if err := fs.Parse([]string{"-sign", "-log", "/tmp/a b.log",
		"-configure", "-terminal=xterm -e",
		"-update_sender=x@example.com", "-read=18f2a",
		"-reply=18f2a", "-continue_draft",
		"-window_file=/tmp/w"}); err != nil {
		t.Fatal(err)
	}
	got := passedOnFlags(fs)
	// -terminal is passed on for the windows a message window opens.
	want := []string{"-log=/tmp/a b.log", "-sign=true",
		"-terminal=xterm -e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWindowCommand(t *testing.T) {
	got := windowCommand(" st  -e ", "/usr/bin/cmdg",
		[]string{"-log=/tmp/l"}, "-reply=18f2a", "/tmp/w.json")
	want := []string{"st", "-e", "/usr/bin/cmdg", "-log=/tmp/l",
		"-reply=18f2a", "-window_file=/tmp/w.json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func fakeConn(t *testing.T) *cmdg.CmdG {
	c, err := cmdg.NewFake(&http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func setTerminal(t *testing.T, s string) {
	old := *terminalFlag
	*terminalFlag = s
	t.Cleanup(func() { *terminalFlag = old })
}

// TestWindowNotStarted checks that a terminal that exits without running
// cmdg is reported, and that the window file is removed.
func TestWindowNotStarted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	term := filepath.Join(dir, "term")
	script := "#!/bin/sh\necho no display >&2\nexit 1\n"
	if err := os.WriteFile(term, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	setTerminal(t, term+" -e")
	errs := make(chan error, 1)
	err := startWindow(fakeConn(t), "-compose", nil, errs)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "did not start") {
			t.Errorf("got error %q, want it to say it did not "+
				"start", err)
		}
		if !strings.Contains(err.Error(), "no display") {
			t.Errorf("got error %q, want it to have the "+
				"terminal's stderr", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no error reported")
	}
	left, err := filepath.Glob(filepath.Join(dir, "cmdg-window-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("window files left: %q", left)
	}
}

// TestWindowStarted runs a fake terminal that reads the window file as
// cmdg -read does, and checks that it has the contacts and messages, and
// that nothing is reported.
func TestWindowStarted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	out := filepath.Join(dir, "state")
	term := filepath.Join(dir, "term")
	// The fake terminal finds -window_file among its arguments, and
	// moves that file to out, as readWindowFile removes it.
	script := `#!/bin/sh
for a; do
	case "$a" in
	-window_file=*) mv "${a#-window_file=}" ` + out + `.tmp &&
		mv ` + out + `.tmp ` + out + `;;
	esac
done
`
	if err := os.WriteFile(term, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	setTerminal(t, term+" -e")
	conn := fakeConn(t)
	conn.SetContacts([]string{"me", "a@example.com"})
	errs := make(chan error, 1)
	err := startWindow(conn, "-read=m2", []string{"m1", "m2"}, errs)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(out); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake terminal did not get the window file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, err := readWindowFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := &windowState{
		Contacts: []string{"me", "a@example.com"},
		Messages: []string{"m1", "m2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("readWindowFile left the file: %v", err)
	}
	select {
	case err := <-errs:
		t.Errorf("got error %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestComposeSomewhereHere checks that without a terminal, composing is
// done by calling here, and its error is reported.
func TestComposeSomewhereHere(t *testing.T) {
	setTerminal(t, "")
	errs := make(chan error, 1)
	called := false
	composeSomewhere("-reply=x", "replying", func() error {
		called = true
		return errors.New("no network")
	}, errs, nil)
	if !called {
		t.Fatal("here was not called")
	}
	select {
	case err := <-errs:
		want := "Failed replying: no network"
		if got := err.Error(); got != want {
			t.Errorf("got error %q, want %q", got, want)
		}
	default:
		t.Error("no error reported")
	}
}
