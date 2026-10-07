package main

// Composing in a window of its own. "c" in the message list starts a new
// terminal running "cmdg -compose", a separate process that asks for the
// addresses, runs the editor, attaches and sends, and then exits, so the
// mail stays usable while a message is being written, and any number of
// messages can be written at once.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
	"github.com/ThomasHabets/cmdg/pkg/display"
	"github.com/ThomasHabets/cmdg/pkg/input"
)

var (
	terminalFlag = flag.String("terminal", "st -e", "Command that "+
		"opens a new terminal running the command appended to it, "+
		"used to compose in a window of its own. Empty to compose in "+
		"the same terminal, which is also done when neither $DISPLAY "+
		"nor $WAYLAND_DISPLAY is set.")
	composeFlag = flag.Bool("compose", false, "Compose one new "+
		"message, then exit.")
	contactsFile = flag.String("contacts_file", "", "With -compose: "+
		"read the address suggestions from this file, a JSON list "+
		"of strings, and delete it, rather than loading Google "+
		"contacts.")

	// notPassedOn are the flags a compose window is not given: those
	// that do something once and exit or change settings, and those
	// that startComposeWindow sets itself.
	notPassedOn = map[string]bool{
		"configure":        true,
		"license":          true,
		"version":          true,
		"update_signature": true,
		"update_sender":    true,
		"terminal":         true,
		"compose":          true,
		"contacts_file":    true,
	}
)

// composeInWindow says whether to compose in a new terminal window.
func composeInWindow() bool {
	if strings.TrimSpace(*terminalFlag) == "" {
		return false
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

// passedOnFlags returns the flags set on fs, other than notPassedOn, in a
// form that sets them again on another command line.
func passedOnFlags(fs *flag.FlagSet) []string {
	var ret []string
	fs.Visit(func(f *flag.Flag) {
		if notPassedOn[f.Name] {
			return
		}
		ret = append(ret,
			fmt.Sprintf("-%s=%s", f.Name, f.Value.String()))
	})
	return ret
}

// composeWindowCommand returns the command line that opens a compose
// window, which runs exe with this process's flags.
func composeWindowCommand(terminal, exe string, flags []string,
	contacts string) []string {
	argv := strings.Fields(terminal)
	argv = append(argv, exe)
	argv = append(argv, flags...)
	return append(argv, "-compose", "-contacts_file="+contacts)
}

// startComposeWindow opens a compose window, with conn's current address
// suggestions, and returns once the terminal has been started. If the
// window fails to start, the error is sent to errs later.
func startComposeWindow(conn *cmdg.CmdG, errs chan<- error) error {
	exe, err := os.Executable()
	if err != nil {
		return errors.Wrap(err, "finding the cmdg binary")
	}

	// The address suggestions are passed in a file rather than on the
	// command line, where they would be visible to other users, and
	// where a single argument is limited to 128KiB on Linux.
	f, err := os.CreateTemp("", "cmdg-contacts-*.json")
	if err != nil {
		return errors.Wrap(err, "creating contacts file")
	}
	if err := json.NewEncoder(f).Encode(conn.Contacts()); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return errors.Wrapf(err, "writing contacts file %q", f.Name())
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return errors.Wrapf(err, "closing contacts file %q", f.Name())
	}

	argv := composeWindowCommand(*terminalFlag, exe,
		passedOnFlags(flag.CommandLine), f.Name())
	cmd := exec.Command(argv[0], argv[1:]...)
	// A session of its own keeps the window open when this terminal
	// is closed, which sends SIGHUP to the processes in its session.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = os.Remove(f.Name())
		return errors.Wrapf(err, "starting terminal %q", argv[0])
	}
	log.Infof("Started compose window, pid %d: %q", cmd.Process.Pid, argv)

	go func() {
		werr := cmd.Wait()
		log.Infof("Compose window pid %d exited: %v, stderr %q",
			cmd.Process.Pid, werr, stderr.String())
		// The compose window deletes the contacts file as it
		// starts, so if the file is still there it never did.
		err := os.Remove(f.Name())
		if err == nil {
			errs <- fmt.Errorf("compose window did not start: %q "+
				"exited (%v), saying %q", argv[0], werr,
				strings.TrimSpace(stderr.String()))
		} else if !os.IsNotExist(err) {
			log.Errorf("Failed to remove contacts file: %v", err)
		}
	}()
	return nil
}

// readContactsFile reads and deletes a file written by startComposeWindow.
func readContactsFile(fn string) ([]string, error) {
	b, err := os.ReadFile(fn)
	if err != nil {
		return nil, errors.Wrap(err, "reading contacts file")
	}
	if err := os.Remove(fn); err != nil {
		log.Errorf("Failed to remove contacts file %q: %v", fn, err)
	}
	var contacts []string
	if err := json.Unmarshal(b, &contacts); err != nil {
		return nil, errors.Wrapf(err, "parsing contacts file %q", fn)
	}
	return contacts, nil
}

// waitToClose keeps the window open until Enter is pressed, so that a
// message printed before it can be read. The terminal must not be in raw
// mode.
func waitToClose() {
	fmt.Fprint(os.Stderr, "\nPress Enter to close this window.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// composeMain is main for -compose: compose and send one message, then
// return.
func composeMain(ctx context.Context) error {
	fmt.Print(display.TerminalTitle("cmdg compose"))
	defer fmt.Print(display.TerminalTitle("Terminal"))

	var contacts []string
	if *contactsFile != "" {
		var err error
		contacts, err = readContactsFile(*contactsFile)
		if err != nil {
			return err
		}
	} else {
		if err := conn.LoadContacts(ctx); err != nil {
			return errors.Wrap(err, "loading contacts")
		}
		contacts = conn.Contacts()
	}

	// The signature and the default sender are needed only once the
	// addresses have been typed in, so they load in the meantime.
	var wg sync.WaitGroup
	var sigErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		sigErr = loadSignature(ctx)
	}()
	go func() {
		defer wg.Done()
		if err := conn.LoadSettings(ctx); err != nil {
			log.Errorf("Failed to load settings: %v", err)
		}
	}()
	ready := func() error {
		wg.Wait()
		return errors.Wrap(sigErr, "loading signature")
	}

	keys := input.New()
	if err := keys.Start(); err != nil {
		return err
	}
	err := composeNew(ctx, conn, contacts, ready, keys)
	keys.Stop()
	display.Exit()
	return err
}
