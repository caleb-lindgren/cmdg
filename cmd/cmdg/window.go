package main

// Windows of their own. Opening a message, composing, replying,
// forwarding and continuing a draft each start a new terminal running
// cmdg with -read, -compose, -reply, -reply_all, -forward or
// -continue_draft: a separate process that shows the message, or asks for
// the addresses, runs the editor, attaches and sends, and then exits. The
// message list stays usable meanwhile, and any number of windows can be
// open at once.
//
// A message window tells the message list that opened it what it changed,
// such as archiving the message, by a Unix socket the list listens on, so
// that the list shows the change at once rather than at its next history
// check, up to messageListHistoryCheckTime later.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
	"github.com/ThomasHabets/cmdg/pkg/display"
	"github.com/ThomasHabets/cmdg/pkg/input"
)

var (
	terminalFlag = flag.String("terminal", "st -e", "Command that "+
		"opens a new terminal running the command appended to it, "+
		"used to read and compose messages in windows of their own. "+
		"Empty to do so in the same terminal, which is also done "+
		"when neither $DISPLAY nor $WAYLAND_DISPLAY is set.")
	readFlag = flag.String("read", "", "Show the message with this "+
		"ID, then exit.")
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
	windowFile = flag.String("window_file", "", "With -read, -compose "+
		"and the other flags that do one thing and exit: read the "+
		"address suggestions, and for -read the IDs of the messages "+
		"^N and ^P move through, and the OAuth access token to "+
		"start from, from this file, and delete it, rather than "+
		"loading Google contacts. It is a JSON object with lists "+
		`of strings "contacts" and "messages", and strings `+
		`"access_token" and "token_expiry".`)

	// notPassedOn are the flags a window is not given: those that do
	// something once and exit or change settings, and those that
	// startWindow sets itself.
	notPassedOn = map[string]bool{
		"configure":        true,
		"license":          true,
		"version":          true,
		"update_signature": true,
		"update_sender":    true,
		"read":             true,
		"compose":          true,
		"reply":            true,
		"reply_all":        true,
		"forward":          true,
		"continue_draft":   true,
		"window_file":      true,
	}
)

// windowState is what a window is handed by the process opening it.
type windowState struct {
	// Contacts are the address suggestions, as Contacts returns them.
	Contacts []string `json:"contacts"`

	// Messages are the IDs of the messages in the message list, in
	// order, for -read.
	Messages []string `json:"messages,omitempty"`

	// AccessToken is the OAuth access token of the process opening
	// the window, and TokenExpiry when it expires, so that the window
	// need not fetch one before its first RPC, which took 210-290ms in
	// logs/window_timing.log.
	// The refresh token is not passed: the window reads it from the
	// config, as the opening process did.
	AccessToken string    `json:"access_token,omitempty"`
	TokenExpiry time.Time `json:"token_expiry,omitzero"`

	// Notify is the path of the socket to send windowEvents to, for
	// -read, or empty to send none.
	Notify string `json:"notify,omitempty"`
}

// windowEvent is what a message window tells the message list that opened
// it about a change it made to a message.
type windowEvent struct {
	// ID is the ID of the message changed.
	ID string `json:"id"`

	// AddLabels and RemoveLabels are the IDs of the labels added to
	// it and removed from it.
	AddLabels    []string `json:"add_labels,omitempty"`
	RemoveLabels []string `json:"remove_labels,omitempty"`

	// Drop says to take the message out of the list whatever its
	// labels, as archiving or deleting a message opened in the same
	// terminal does.
	Drop bool `json:"drop,omitempty"`
}

// windowEventListener listens for windowEvents on a Unix socket.
type windowEventListener struct {
	dir string
	l   net.Listener
}

// listenWindowEvents listens for windowEvents on a new Unix socket, and
// sends each one received to events. The socket is in a directory only
// this user can open.
func listenWindowEvents(events chan<- windowEvent) (*windowEventListener,
	error) {
	dir, err := os.MkdirTemp("", "cmdg-events-*")
	if err != nil {
		return nil, errors.Wrap(err, "creating window event directory")
	}
	l, err := net.Listen("unix", filepath.Join(dir, "socket"))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, errors.Wrap(err, "listening for window events")
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				log.Infof("No longer listening for window "+
					"events: %v", err)
				return
			}
			go readWindowEvents(c, events)
		}
	}()
	return &windowEventListener{dir: dir, l: l}, nil
}

// readWindowEvents sends the windowEvents read from c to events, until c
// is closed.
func readWindowEvents(c net.Conn, events chan<- windowEvent) {
	defer func() { _ = c.Close() }()
	dec := json.NewDecoder(c)
	for {
		var ev windowEvent
		if err := dec.Decode(&ev); err == io.EOF {
			return
		} else if err != nil {
			log.Errorf("Failed to read window event: %v", err)
			return
		}
		log.Infof("Got window event: %+v", ev)
		events <- ev
	}
}

// Path returns the path of the socket.
func (w *windowEventListener) Path() string {
	return w.l.Addr().String()
}

// Close stops listening, and removes the socket.
func (w *windowEventListener) Close() error {
	err := w.l.Close()
	if rmErr := os.RemoveAll(w.dir); err == nil {
		err = rmErr
	}
	return err
}

var (
	// windowEvents gets the windowEvents sent to this process, for
	// the message list to apply. The buffer is so that a window does
	// not wait while the list is busy; a history check picks up any
	// that the list never takes.
	windowEvents = make(chan windowEvent, 100)

	windowEventsOnce     sync.Once
	windowEventsListener *windowEventListener
)

// windowEventSocket returns the path of the socket that sends to
// windowEvents, listening on it the first time it is called, or empty if
// that failed.
func windowEventSocket() string {
	windowEventsOnce.Do(func() {
		var err error
		windowEventsListener, err = listenWindowEvents(windowEvents)
		if err != nil {
			log.Errorf("Message windows will not update the "+
				"message list until its next history "+
				"check: %v", err)
		}
	})
	if windowEventsListener == nil {
		return ""
	}
	return windowEventsListener.Path()
}

// closeWindowEventSocket stops listening on the socket windowEventSocket
// returned, if it was called.
func closeWindowEventSocket() {
	// Do rather than a plain read, so that a listener still starting
	// is waited for, and none starts after this.
	windowEventsOnce.Do(func() {})
	if windowEventsListener == nil {
		return
	}
	if err := windowEventsListener.Close(); err != nil {
		log.Errorf("Failed to close window event socket: %v", err)
	}
}

// tellOpener sends ev to the socket at path, of the message list that
// opened this window.
func tellOpener(path string, ev windowEvent) error {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return errors.Wrap(err, "connecting to the message list")
	}
	if err := c.SetDeadline(time.Now().Add(time.Second)); err != nil {
		_ = c.Close()
		return errors.Wrap(err, "setting deadline")
	}
	if err := json.NewEncoder(c).Encode(&ev); err != nil {
		_ = c.Close()
		return errors.Wrap(err, "sending to the message list")
	}
	return c.Close()
}

// windowFlagsSet returns how many of -read, -compose, -reply,
// -reply_all, -forward and -continue_draft are set.
func windowFlagsSet() int {
	n := 0
	for _, set := range []bool{*readFlag != "", *composeFlag,
		*replyFlag != "", *replyAllFlag != "", *forwardFlag != "",
		*continueDraftFlag} {
		if set {
			n++
		}
	}
	return n
}

// windowOnly says whether cmdg was started to do one thing and exit, by
// -read, -compose, -reply, -reply_all, -forward or -continue_draft.
func windowOnly() bool {
	return windowFlagsSet() > 0
}

// useWindows says whether to open messages and compose in new terminal
// windows.
func useWindows() bool {
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

// windowCommand returns the command line that opens a window, which runs
// exe with this process's flags, what, the flag saying what to do, and
// the window file fn.
func windowCommand(terminal, exe string, flags []string,
	what, fn string) []string {
	argv := strings.Fields(terminal)
	argv = append(argv, exe)
	argv = append(argv, flags...)
	return append(argv, what, "-window_file="+fn)
}

// startWindow opens a window, running cmdg with the flag what, such as
// "-compose" or "-reply=<message ID>", conn's current address
// suggestions, the IDs of the messages listed, and the path of the socket
// to send windowEvents to, notify, if not empty. It returns once the
// terminal has been started. If the window fails to start, the error is
// sent to errs later.
func startWindow(conn *cmdg.CmdG, what string, messages []string,
	notify string, errs chan<- error) error {
	exe, err := os.Executable()
	if err != nil {
		return errors.Wrap(err, "finding the cmdg binary")
	}

	// The address suggestions are passed in a file rather than on the
	// command line, where they would be visible to other users, and
	// where a single argument is limited to 128KiB on Linux.
	f, err := os.CreateTemp("", "cmdg-window-*.json")
	if err != nil {
		return errors.Wrap(err, "creating window file")
	}
	state := windowState{Contacts: conn.Contacts(), Messages: messages,
		Notify: notify}
	state.AccessToken, state.TokenExpiry = conn.AccessToken()
	if err := json.NewEncoder(f).Encode(&state); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return errors.Wrapf(err, "writing window file %q", f.Name())
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return errors.Wrapf(err, "closing window file %q", f.Name())
	}

	argv := windowCommand(*terminalFlag, exe,
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
	log.Infof("Started window, pid %d: %q", cmd.Process.Pid, argv)

	go func() {
		werr := cmd.Wait()
		log.Infof("Window pid %d exited: %v, stderr %q",
			cmd.Process.Pid, werr, stderr.String())
		// The window deletes the window file as it starts, so if
		// the file is still there it never did.
		err := os.Remove(f.Name())
		if err == nil {
			errs <- fmt.Errorf("window did not start: %q exited "+
				"(%v), saying %q", argv[0], werr,
				strings.TrimSpace(stderr.String()))
		} else if !os.IsNotExist(err) {
			log.Errorf("Failed to remove window file: %v", err)
		}
	}()
	return nil
}

// readWindowFile reads and deletes a file written by startWindow.
func readWindowFile(fn string) (*windowState, error) {
	b, err := os.ReadFile(fn)
	if err != nil {
		return nil, errors.Wrap(err, "reading window file")
	}
	if err := os.Remove(fn); err != nil {
		log.Errorf("Failed to remove window file %q: %v", fn, err)
	}
	var state windowState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, errors.Wrapf(err, "parsing window file %q", fn)
	}
	return &state, nil
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
	if useWindows() {
		err := startWindow(conn, what, nil, "", windowErrs)
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

// windowMain is main for -read and the other flags that do one thing
// and exit: do it, then return.
func windowMain(ctx context.Context) error {
	title := "cmdg compose"
	if *readFlag != "" {
		title = "cmdg message"
	}
	fmt.Print(display.TerminalTitle(title))
	defer fmt.Print(display.TerminalTitle("Terminal"))

	var state *windowState
	if *windowFile != "" {
		var err error
		if state, err = readWindowFile(*windowFile); err != nil {
			return err
		}
		// For windows started from this one.
		conn.SetContacts(state.Contacts)
		if state.AccessToken != "" {
			conn.SetAccessToken(state.AccessToken,
				state.TokenExpiry)
			log.Infof("Using the access token of the process "+
				"that opened this window, expiring %v",
				state.TokenExpiry)
		}
	} else {
		if err := conn.LoadContacts(ctx); err != nil {
			return errors.Wrap(err, "loading contacts")
		}
		state = &windowState{Contacts: conn.Contacts()}
	}

	if *readFlag != "" {
		return readMain(ctx, *readFlag, state.Messages, state.Notify)
	}
	return composeMain(ctx, state.Contacts)
}

// readMain shows the message with the ID id, and, by ^N and ^P, the
// messages before and after it in ids. It tells the changes it makes to
// them to the socket at notify, if not empty.
func readMain(ctx context.Context, id string, ids []string,
	notify string) error {
	pos := -1
	for n, i := range ids {
		if i == id {
			pos = n
			break
		}
	}
	if pos < 0 {
		ids, pos = []string{id}, 0
	}

	// The labels are needed to show the message's, and the message is
	// fetched in the meantime.
	msg := cmdg.NewMessage(conn, id)
	go func() {
		if err := msg.Preload(ctx, cmdg.LevelFull); err != nil {
			log.Errorf("Failed to load message %q: %v", id, err)
		}
	}()
	if err := conn.LoadLabels(ctx); err != nil {
		return errors.Wrap(err, "loading labels")
	}

	keys := input.New()
	if err := keys.Start(); err != nil {
		return err
	}
	defer display.Exit()
	defer keys.Stop()
	for {
		ov, err := NewOpenMessageView(ctx,
			cmdg.NewMessage(conn, ids[pos]), keys)
		if err != nil {
			return errors.Wrap(err, "opening message")
		}
		if notify != "" {
			ov.changed = func(ev windowEvent) {
				if err := tellOpener(notify, ev); err != nil {
					log.Errorf("Failed to tell the "+
						"message list of %+v: %v",
						ev, err)
				}
			}
		}
		op, err := ov.Run(ctx)
		if err != nil {
			return err
		}
		// As in the message list, ^N on the last message and ^P on
		// the first show it again.
		switch {
		case op.IsNext(nil):
			if pos < len(ids)-1 {
				pos++
			}
		case op.IsPrev(nil):
			if pos > 0 {
				pos--
			}
		default:
			// Closing the message, quitting, archiving,
			// deleting and marking unread all close the
			// window. The message list was told of the
			// changes, or else sees them in its next history
			// check.
			return nil
		}
	}
}

// composeMain composes and sends one message, as given by -compose,
// -reply, -reply_all, -forward or -continue_draft, suggesting contacts as
// recipients.
func composeMain(ctx context.Context, contacts []string) error {
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
