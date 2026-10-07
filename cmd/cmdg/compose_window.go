package main

// Composing in a window of its own. Composing, replying, forwarding and
// continuing a draft each start a new terminal running cmdg with -compose,
// -reply, -reply_all, -forward or -continue_draft, a separate process that
// asks for the addresses, runs the editor, attaches and sends, and then
// exits, so the mail stays usable while a message is being written, and
// any number of messages can be written at once.

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
	replyFlag = flag.String("reply", "", "Reply to the message with "+
		"this ID, then exit.")
	replyAllFlag = flag.String("reply_all", "", "Reply to all of the "+
		"message with this ID, then exit.")
	forwardFlag = flag.String("forward", "", "Forward the message "+
		"with this ID, then exit.")
	continueDraftFlag = flag.Bool("continue_draft", false, "Choose a "+
		"draft, continue it, then exit.")
	contactsFile = flag.String("contacts_file", "", "With -compose "+
		"and the other flags that compose and exit: read the "+
		"address suggestions from this file, a JSON list of "+
		"strings, and delete it, rather than loading Google "+
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
		"reply":            true,
		"reply_all":        true,
		"forward":          true,
		"continue_draft":   true,
		"contacts_file":    true,
	}
)

// composeFlagsSet returns how many of -compose, -reply, -reply_all,
// -forward and -continue_draft are set.
func composeFlagsSet() int {
	n := 0
	for _, set := range []bool{*composeFlag, *replyFlag != "",
		*replyAllFlag != "", *forwardFlag != "", *continueDraftFlag} {
		if set {
			n++
		}
	}
	return n
}

// composeOnly says whether cmdg was started to compose one message and
// exit, by -compose, -reply, -reply_all, -forward or -continue_draft.
func composeOnly() bool {
	return composeFlagsSet() > 0
}

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
// window, which runs exe with this process's flags and what, the flag
// saying what to compose.
func composeWindowCommand(terminal, exe string, flags []string,
	what, contacts string) []string {
	argv := strings.Fields(terminal)
	argv = append(argv, exe)
	argv = append(argv, flags...)
	return append(argv, what, "-contacts_file="+contacts)
}

// startComposeWindow opens a compose window, running cmdg with the flag
// what, such as "-compose" or "-reply=<message ID>", and conn's current
// address suggestions. It returns once the terminal has been started. If
// the window fails to start, the error is sent to errs later.
func startComposeWindow(conn *cmdg.CmdG, what string,
	errs chan<- error) error {
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
		passedOnFlags(flag.CommandLine), what, f.Name())
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

// composeSomewhere composes in a new window, running cmdg with the flag
// what, if there can be one, and by calling here otherwise. Errors go to
// errs, except that a window failing after it was started goes to
// windowErrs, so that it can be shown after the view that started it has
// closed. doing names what is composed, for those errors.
func composeSomewhere(what, doing string, here func() error,
	errs, windowErrs chan<- error) {
	if composeInWindow() {
		err := startComposeWindow(conn, what, windowErrs)
		if err != nil {
			errs <- errors.Wrapf(err, "Opening window for %s",
				doing)
		}
		return
	}
	if err := here(); err != nil {
		errs <- errors.Wrapf(err, "Failed %s", doing)
	}
}

// composeMain is main for -compose and the other flags that compose one
// message: compose and send it, then return.
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

	// A message replied to or forwarded is fetched in the meantime as
	// well.
	var msg *cmdg.Message
	if id := *replyFlag + *replyAllFlag + *forwardFlag; id != "" {
		msg = cmdg.NewMessage(conn, id)
		go func() {
			err := msg.Preload(ctx, cmdg.LevelFull)
			if err != nil {
				log.Errorf("Failed to load message %q: %v",
					id, err)
			}
		}()
	}

	keys := input.New()
	if err := keys.Start(); err != nil {
		return err
	}
	err := composeOne(ctx, contacts, ready, keys, msg)
	keys.Stop()
	display.Exit()
	return err
}

// composeOne composes what the flags say to, with the message msg given
// by -reply, -reply_all or -forward. ready waits for the signature and the
// settings to load.
func composeOne(ctx context.Context, contacts []string, ready func() error,
	keys *input.Input, msg *cmdg.Message) error {
	switch {
	case *replyFlag != "":
		if err := ready(); err != nil {
			return err
		}
		return reply(ctx, conn, keys, msg)
	case *replyAllFlag != "":
		if err := ready(); err != nil {
			return err
		}
		return replyAll(ctx, conn, keys, msg)
	case *forwardFlag != "":
		return forward(ctx, conn, contacts, ready, keys, msg)
	case *continueDraftFlag:
		return continueDraft(ctx, conn, keys)
	default:
		return composeNew(ctx, conn, contacts, ready, keys)
	}
}
