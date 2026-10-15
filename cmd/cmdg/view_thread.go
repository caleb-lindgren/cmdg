package main

// The conversation view, added by this fork: every message of a thread,
// oldest first, one after the other, with quoted text folded away. See
// conversations.go for the message list side.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/ThomasHabets/cmdg/internal/customize"
	"github.com/ThomasHabets/cmdg/pkg/cmdg"
	"github.com/ThomasHabets/cmdg/pkg/dialog"
	"github.com/ThomasHabets/cmdg/pkg/display"
	"github.com/ThomasHabets/cmdg/pkg/input"
)

const (
	spamLabel  = "SPAM"
	draftLabel = "DRAFT"

	// threadHeaderLines is how many lines Draw uses above the
	// messages, and the two below them are the bottom rule and the
	// status line.
	threadHeaderLines = 4

	threadViewHelp = `?, F1     — Help
^R             — Reload
z              — Show or hide quoted text of the selected message
j              — Select next message
k              — Select previous message
l              — Add label to the conversation
L              — Remove label from the conversation
*              — Toggle "starred"
u, ←           — Exit conversation
U              — Mark unread
J, Down        — Scroll down
space          — Page down
backspace      — Page up
K, Up          — Scroll up
^P             — Previous conversation
^N             — Next conversation
f              — Forward the selected message, in a new window
r              — Reply to the selected message, in a new window
s, ^s          — Search within conversation
a              — Reply all to the selected message, in a new window
d              — Delete conversation
e              — Archive conversation
t, →           — Browse attachments of the selected message
H              — Force HTML view
\              — Show raw source of the selected message

Press [enter] to exit
`
)

// nextMessageKey and prevMessageKey are what threadKey turns the keys
// selecting the next and previous message into.
const (
	nextMessageKey = "\x00thread-next-message"
	prevMessageKey = "\x00thread-prev-message"
)

// threadKey translates a key pressed in the conversation view as the
// open message view does, but for j and k, which select the next and
// previous message, and J and K, which scroll by a line as j and k do in
// the open message view.
func threadKey(key string) string {
	switch key {
	case "j":
		return nextMessageKey
	case "k":
		return prevMessageKey
	case "J":
		return "n"
	case "K":
		return "p"
	}
	return customize.OpenMessageKey(key)
}

// messageViewer is a view of an opened row of the message list.
type messageViewer interface {
	Run(ctx context.Context) (*MessageViewOp, error)
}

// openViewer returns the view of msg: the conversation it is in if
// conversation is set, and the message alone otherwise. windowErrors and
// changed are as in OpenMessageView.
func openViewer(ctx context.Context, msg *cmdg.Message, keys *input.Input,
	conversation bool, windowErrors chan error,
	changed func(windowEvent)) (messageViewer, error) {
	if conversation {
		tv, err := NewThreadView(ctx, msg, keys)
		if err != nil {
			return nil, err
		}
		tv.ov.windowErrors = windowErrors
		tv.ov.changed = changed
		return tv, nil
	}
	ov, err := NewOpenMessageView(ctx, msg, keys)
	if err != nil {
		return nil, err
	}
	ov.windowErrors = windowErrors
	ov.changed = changed
	return ov, nil
}

// ThreadView is the view of a conversation.
type ThreadView struct {
	// ov does what the conversation view does the same way as the
	// open message view, on ov.msg, the selected message: replying,
	// forwarding, attachments, the raw source and searching. Its
	// screen is this view's.
	ov  *OpenMessageView
	rep *cmdg.Message

	loaded chan []*cmdg.Message
	// polled is sent what each check of the thread for new messages
	// found.
	polled chan polledThread

	// Main goroutine only.
	msgs   []*cmdg.Message
	lines  []string
	starts []int // Index in lines of each message's first line.
	// sel is the index in msgs of the selected message, the one
	// replies, z and the like act on, whose sender line is
	// highlighted.
	sel int
	// showQuotes holds the IDs of the messages whose quoted text is
	// shown.
	showQuotes map[string]bool
	preferHTML bool
	// arrived holds the messages that arrived while the conversation
	// was open, and have not been read yet.
	arrived arrivals
}

// polledThread is what a check of the thread found: the ID and labels of
// each of its messages.
type polledThread struct {
	ids    []string
	labels [][]string
}

// NewThreadView creates the view of the conversation of rep, and starts
// loading it.
func NewThreadView(ctx context.Context, rep *cmdg.Message,
	in *input.Input) (*ThreadView, error) {
	screen, err := display.NewScreen()
	if err != nil {
		return nil, err
	}
	tv := &ThreadView{
		ov: &OpenMessageView{
			msg:    rep,
			keys:   in,
			screen: screen,
			update: make(chan struct{}),
			errors: make(chan error, 20),
		},
		rep:        rep,
		loaded:     make(chan []*cmdg.Message, 1),
		polled:     make(chan polledThread),
		showQuotes: map[string]bool{},
	}
	tv.ov.drawLines = tv.draw
	go tv.load(ctx, false)
	return tv, nil
}

// load loads the messages of the conversation, reloading those already
// loaded if reload is set, and sends them to tv.loaded.
func (tv *ThreadView) load(ctx context.Context, reload bool) {
	st := time.Now()
	tid, err := tv.rep.ThreadID(ctx)
	if err != nil {
		tv.ov.errors <- errors.Wrap(err, "getting thread ID")
		return
	}
	msgs, err := conn.ThreadMessages(ctx, tid)
	if err != nil {
		tv.ov.errors <- err
		return
	}
	if reload {
		for _, m := range msgs {
			if err := m.Reload(ctx, cmdg.LevelFull); err != nil {
				tv.ov.errors <- errors.Wrapf(err,
					"reloading message %q", m.ID)
				return
			}
		}
	}
	log.Infof("Got %d messages of thread %q in %v", len(msgs), tid,
		time.Since(st))
	tv.loaded <- visibleMessages(msgs, tv.rep)
}

// watch checks the thread every threadPollTime until ctx is done, and
// sends what it found to tv.polled. A check that fails is logged only, as
// the next may well succeed, and the conversation shown is still right.
func (tv *ThreadView) watch(ctx context.Context) {
	t := time.NewTicker(threadPollTime)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		tid, err := tv.rep.ThreadID(ctx)
		if err != nil {
			log.Warningf("Checking thread: getting thread ID: %v",
				err)
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, threadFetchTimeout)
		ids, labels, err := conn.ThreadMessageLabels(pctx, tid)
		cancel()
		if err != nil {
			log.Warningf("Checking thread %q for new messages: %v",
				tid, err)
			continue
		}
		select {
		case tv.polled <- polledThread{ids, labels}:
		case <-ctx.Done():
			return
		}
	}
}

// hidden returns whether a message with the labels labels is left out of
// the conversation of rep: whether it is in the trash or marked as spam,
// and rep is not, as Gmail shows a conversation.
func hidden(labels []string, rep *cmdg.Message) bool {
	for _, l := range labels {
		if (l == cmdg.Trash || l == spamLabel) && !rep.HasLabel(l) {
			return true
		}
	}
	return false
}

// visibleMessages returns msgs without those hidden in the conversation
// of rep, or all of them if all are.
func visibleMessages(msgs []*cmdg.Message, rep *cmdg.Message) []*cmdg.Message {
	var ret []*cmdg.Message
	for _, m := range msgs {
		if !hidden(m.LocalLabels(), rep) {
			ret = append(ret, m)
		}
	}
	if len(ret) == 0 {
		return msgs
	}
	return ret
}

// visibleCount returns how many messages visibleMessages would return of
// messages with the labels labels.
func visibleCount(labels [][]string, rep *cmdg.Message) int {
	n := 0
	for _, ls := range labels {
		if !hidden(ls, rep) {
			n++
		}
	}
	if n == 0 {
		return len(labels)
	}
	return n
}

// targets returns the messages an action on the conversation applies to
// locally: those loaded, or the one opened if none are yet.
func (tv *ThreadView) targets() []*cmdg.Message {
	if len(tv.msgs) == 0 {
		return []*cmdg.Message{tv.rep}
	}
	return tv.msgs
}

// tell tells the message list of a window that labels add were added to
// and remove removed from msg, and whether to drop it from the list.
func (tv *ThreadView) tell(msg *cmdg.Message, add, remove []string,
	drop bool) {
	if tv.ov.changed == nil {
		return
	}
	tv.ov.changed(windowEvent{
		ID:           msg.ID,
		AddLabels:    add,
		RemoveLabels: remove,
		Drop:         drop,
	})
}

// modify adds labels add to and removes labels remove from every message
// in the conversation, and tells the message list.
func (tv *ThreadView) modify(ctx context.Context, add, remove []string,
	drop bool) error {
	tid, err := tv.rep.ThreadID(ctx)
	if err != nil {
		return errors.Wrap(err, "getting thread ID")
	}
	if err := conn.ModifyThread(ctx, tid, add, remove); err != nil {
		return err
	}
	for _, m := range tv.targets() {
		for _, l := range add {
			m.AddLabelIDLocal(l)
		}
		for _, l := range remove {
			m.RemoveLabelIDLocal(l)
		}
		tv.tell(m, add, remove, drop)
	}
	return nil
}

// markRead marks the unread messages of the conversation read, but for
// those that have arrived while it was open, which are marked read once
// they have been read, by markArrivalsRead.
func (tv *ThreadView) markRead(ctx context.Context) {
	var msgs []*cmdg.Message
	for _, m := range tv.msgs {
		if !tv.arrived.has(m.ID) {
			msgs = append(msgs, m)
		}
	}
	tv.markMessagesRead(ctx, msgs)
}

// markArrivalsRead marks read the messages with the IDs ids, which have
// arrived while the conversation was open, and been read.
func (tv *ThreadView) markArrivalsRead(ctx context.Context, ids []string) {
	read := map[string]bool{}
	for _, id := range ids {
		read[id] = true
	}
	var msgs []*cmdg.Message
	for _, m := range tv.msgs {
		if read[m.ID] {
			msgs = append(msgs, m)
		}
	}
	tv.markMessagesRead(ctx, msgs)
}

// markMessagesRead marks the unread messages of msgs read.
func (tv *ThreadView) markMessagesRead(ctx context.Context,
	msgs []*cmdg.Message) {
	var unread []*cmdg.Message
	var ids []string
	for _, m := range msgs {
		if m.IsUnread() {
			unread = append(unread, m)
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	go func() {
		err := conn.BatchUnlabel(ctx, ids, cmdg.Unread)
		if err != nil {
			tv.ov.errors <- errors.Wrap(err,
				"Failed to remove unread label")
			return
		}
		for _, m := range unread {
			m.RemoveLabelIDLocal(cmdg.Unread)
			tv.tell(m, nil, []string{cmdg.Unread}, false)
		}
	}()
}

// labels returns the labels any message in the conversation has.
func (tv *ThreadView) labels(ctx context.Context) ([]*cmdg.Label, error) {
	seen := map[string]bool{}
	var ret []*cmdg.Label
	for _, m := range tv.targets() {
		ls, err := m.GetLabels(ctx, true)
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			if !seen[l.ID] {
				seen[l.ID] = true
				ret = append(ret, l)
			}
		}
	}
	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Label < ret[j].Label
	})
	return ret, nil
}

// wrapLine breaks line into lines at most width columns wide. Escape
// sequences such as colours take no columns, and are not broken.
func wrapLine(line string, width int) []string {
	if width < 1 {
		return []string{line}
	}
	var ret []string
	var cur strings.Builder
	w := 0
	inEscape := false
	for _, r := range line {
		// An escape sequence, ESC [ parameters, ends at a letter.
		if r == '\x1b' || inEscape {
			inEscape = r == '\x1b' || r == '[' ||
				!(r >= '@' && r <= '~')
			cur.WriteRune(r)
			continue
		}
		rw := runewidth.RuneWidth(r)
		if w+rw > width {
			ret = append(ret, cur.String())
			cur.Reset()
			w = 0
		}
		cur.WriteRune(r)
		w += rw
	}
	return append(ret, cur.String())
}

// messageHeader returns the lines put above msg's body.
func messageHeader(ctx context.Context, msg *cmdg.Message) []string {
	from, err := msg.GetHeader(ctx, "From")
	if err != nil {
		from = fmt.Sprintf("Unknown: %q", err)
	}
	var date string
	if t, err := msg.GetOriginalTime(ctx); err != nil {
		date, _ = msg.GetDateHeader(ctx)
	} else {
		date = t.Local().Format(tsLayout)
	}
	var tags string
	if msg.HasLabel(draftLabel) {
		tags += " [draft]"
	}
	if msg.HasLabel(cmdg.Starred) {
		tags += " *"
	}
	if st := msg.GPGStatus(); st != nil && st.Signed != "" {
		if st.GoodSignature {
			tags += " — signed by " + st.Signed
		} else {
			tags += " — BAD signature from " + st.Signed
		}
	}
	ret := []string{
		display.Bold + from + tags + display.Reset,
		fmt.Sprintf("%sDate: %s%s", display.Grey, date, display.Reset),
	}
	for _, h := range []string{"To", "CC"} {
		if v, err := msg.GetHeader(ctx, h); err == nil && v != "" {
			ret = append(ret, fmt.Sprintf("%s%s: %s%s",
				display.Grey, h, v, display.Reset))
		}
	}
	return append(ret, "")
}

// build lays out the loaded messages in tv.lines, width columns wide.
func (tv *ThreadView) build(ctx context.Context, width int) {
	tv.lines = nil
	tv.starts = nil
	for n, m := range tv.msgs {
		if n > 0 {
			tv.lines = append(tv.lines, "", display.Grey+
				strings.Repeat("─", width)+display.Reset)
		}
		tv.starts = append(tv.starts, len(tv.lines))
		tv.lines = append(tv.lines, messageHeader(ctx, m)...)

		gb := m.GetBody
		if tv.preferHTML {
			gb = m.GetBodyHTML
		}
		b, err := gb(ctx)
		if err != nil {
			b = fmt.Sprintf("%sGetting message body: %v%s",
				display.Red, err, display.Reset)
		}
		var body []string
		for _, l := range strings.Split(b, "\n") {
			l = display.ExpandTabs(l)
			body = append(body, strings.TrimRight(l, "\r "))
		}
		for len(body) > 0 && body[len(body)-1] == "" {
			body = body[:len(body)-1]
		}
		if !tv.showQuotes[m.ID] {
			body = foldQuotes(body)
		}
		for _, l := range body {
			tv.lines = append(tv.lines, wrapLine(l, width)...)
		}
	}
}

// spans returns the [start, end) lines of each message of the
// conversation, by ID, without the blank line and rule separating it from
// the next.
func (tv *ThreadView) spans() map[string][2]int {
	ret := map[string][2]int{}
	for n, m := range tv.msgs {
		if n >= len(tv.starts) {
			break
		}
		end := tv.end(n)
		if n+1 < len(tv.starts) {
			end -= 2
		}
		ret[m.ID] = [2]int{tv.starts[n], end}
	}
	return ret
}

// notice returns the line telling of the messages that have arrived and
// not been read, or "" if there are none.
func (tv *ThreadView) notice(ctx context.Context) string {
	ids := tv.arrived.ids()
	if len(ids) == 0 {
		return ""
	}
	pos := 0
	for n, m := range tv.msgs {
		if m.ID == ids[0] {
			pos = n + 1
			break
		}
	}
	var froms []string
	for _, id := range ids {
		from, err := cmdg.NewMessage(conn, id).GetHeader(ctx, "From")
		if err != nil {
			from = "unknown sender"
		}
		froms = append(froms, from)
	}
	return arrivalNotice(froms, pos, len(tv.msgs))
}

// noticeLine returns notice highlighted, from the left edge of a line
// width columns wide, the highlight filling the rest of it, or "" if
// notice is.
func noticeLine(notice string, width int) string {
	if notice == "" {
		return ""
	}
	return display.Reverse + display.Bold + runewidth.FillRight(
		runewidth.Truncate(notice, width, "…"), width) + display.Reset
}

// current returns the index in tv.msgs of the message at line scroll.
func (tv *ThreadView) current(scroll int) int {
	cur := 0
	for n, s := range tv.starts {
		if s <= scroll {
			cur = n
		}
	}
	return cur
}

// end returns the index in tv.lines just past message n.
func (tv *ThreadView) end(n int) int {
	if n+1 < len(tv.starts) {
		return tv.starts[n+1]
	}
	return len(tv.lines)
}

// follow keeps the selection on screen once it has been scrolled to
// scroll: if the selected message is no longer on screen at all, the
// message at the edge it left by is selected.
func (tv *ThreadView) follow(scroll int) {
	if tv.sel >= len(tv.starts) {
		return
	}
	switch {
	case tv.end(tv.sel) <= scroll:
		tv.sel = tv.current(scroll)
	case tv.starts[tv.sel] >= scroll+tv.contentHeight():
		tv.sel = tv.current(scroll + tv.contentHeight() - 1)
	}
}

// contentHeight is how many lines of the conversation fit on the screen.
func (tv *ThreadView) contentHeight() int {
	return max(1, tv.ov.screen.Height-threadHeaderLines-2)
}

// clamp returns scroll kept within the conversation.
func (tv *ThreadView) clamp(scroll int) int {
	return max(0, min(scroll, len(tv.lines)-tv.contentHeight()))
}

// draw draws lines, the conversation or a copy of it with search hits
// marked, scrolled to line scroll, and makes the selected message the one
// ov acts on. While searching, the message at the top is selected.
func (tv *ThreadView) draw(lines []string, scroll int) error {
	ctx := cancelledContext()
	screen := tv.ov.screen
	height := tv.contentHeight()

	if tv.ov.inIncrementalSearch {
		tv.sel = tv.current(scroll)
	}
	tv.sel = max(0, min(tv.sel, len(tv.msgs)-1))
	if len(tv.msgs) > 0 {
		tv.ov.msg = tv.msgs[tv.sel]
	}

	var searching string
	if tv.ov.inIncrementalSearch {
		searching = fmt.Sprintf(
			" Incremental search: %s (at %d of %d)",
			tv.ov.incrementalQuery, tv.ov.incrementalCurrent,
			tv.ov.incrementalCount)
	}
	pct := 100
	if len(lines) > 0 {
		pct = min(100, 100*(scroll+height)/len(lines))
	}
	quotes := "quotes hidden"
	if len(tv.msgs) > 0 && tv.showQuotes[tv.msgs[tv.sel].ID] {
		quotes = "quotes shown"
	}
	if len(tv.msgs) == 0 {
		screen.Printlnf(0, "Loading…")
	} else {
		screen.Printlnf(0,
			"Message %d of %d, %s. Scroll %d-%d/%d (%d%%)%s",
			tv.sel+1, len(tv.msgs), quotes, scroll,
			min(scroll+height, len(lines)), len(lines), pct,
			searching)
	}

	subject, err := tv.rep.GetSubject(ctx)
	if err != nil {
		subject = fmt.Sprintf("Unknown: %q", err)
	}
	screen.Printlnf(1, "Subject: %s", subject)
	labels, err := tv.rep.GetLabelsString(ctx)
	if err != nil {
		labels = fmt.Sprintf("Unknown: %q", err)
	}
	screen.Printlnf(2, "Labels: %s", labels)
	screen.Printlnf(3, "%s", strings.Repeat("—", screen.Width))

	for n := 0; n < height; n++ {
		l := ""
		if scroll+n < len(lines) {
			l = lines[scroll+n]
		}
		if tv.sel < len(tv.starts) && scroll+n == tv.starts[tv.sel] {
			l = display.Reverse + "▶" + display.Reset + " " + l
		}
		screen.Printlnf(threadHeaderLines+n, "%s", l)
	}
	screen.Printlnf(screen.Height-2, "%s",
		strings.Repeat("—", screen.Width))
	screen.Printlnf(screen.Height-1, "%s",
		noticeLine(tv.notice(ctx), screen.Width))
	return nil
}

// chooseLabel asks for one of labels, and returns it, or nil if none was
// chosen.
func (tv *ThreadView) chooseLabel(labels []*cmdg.Label) *dialog.Option {
	var opts []*dialog.Option
	for _, l := range labels {
		opts = append(opts, &dialog.Option{Key: l.ID, Label: l.Label})
	}
	label, err := dialog.Selection(opts, "Label> ", false, tv.ov.keys)
	if errors.Cause(err) == dialog.ErrAborted {
		return nil
	} else if err != nil {
		tv.ov.errors <- errors.Wrapf(err, "Selecting label")
		return nil
	}
	return label
}

// label adds a label to the conversation.
func (tv *ThreadView) label(ctx context.Context) {
	label := tv.chooseLabel(conn.Labels())
	if label == nil {
		return
	}
	err := tv.modify(ctx, []string{label.Key}, nil, false)
	if err != nil {
		tv.ov.errors <- errors.Wrapf(err, "Failed to label")
	}
}

// unlabel removes a label from the conversation.
func (tv *ThreadView) unlabel(ctx context.Context) {
	labels, err := tv.labels(ctx)
	if err != nil {
		tv.ov.errors <- errors.Wrapf(err, "Getting message labels")
		return
	}
	label := tv.chooseLabel(labels)
	if label == nil {
		return
	}
	if err := tv.modify(ctx, nil, []string{label.Key}, false); err != nil {
		tv.ov.errors <- errors.Wrapf(err, "Failed to unlabel")
	}
}

// newest returns the newest message of the conversation, which is the one
// the message list shows for it.
func (tv *ThreadView) newest() *cmdg.Message {
	ts := tv.targets()
	return ts[len(ts)-1]
}

// toggleStar unstars every message of the conversation if any is starred,
// and stars the newest otherwise, as Gmail does.
func (tv *ThreadView) toggleStar(ctx context.Context) {
	for _, m := range tv.targets() {
		if !m.HasLabel(cmdg.Starred) {
			continue
		}
		err := tv.modify(ctx, nil, []string{cmdg.Starred}, false)
		if err != nil {
			tv.ov.errors <- errors.Wrap(err,
				"Removing STARRED label")
		}
		return
	}
	m := tv.newest()
	if err := m.AddLabelID(ctx, cmdg.Starred); err != nil {
		tv.ov.errors <- errors.Wrap(err, "Adding STARRED label")
		return
	}
	tv.tell(m, []string{cmdg.Starred}, nil, false)
}

// attachments browses the attachments of the selected message.
func (tv *ThreadView) attachments(ctx context.Context) {
	ov := tv.ov
	as, err := ov.msg.Attachments(ctx)
	if err != nil {
		ov.errors <- fmt.Errorf("Listing attachments failed: %v", err)
		return
	}
	if len(as) == 0 {
		return
	}
	err = listAttachments(ctx, ov.keys, ov.msg)
	if errors.Cause(err) == dialog.ErrAborted {
		log.Infof("View attachment aborted")
	} else if err != nil {
		ov.errors <- fmt.Errorf(
			"Attachment browser action failed: %v", err)
	}
}

// startAt returns the message to show first: the first unread one, as
// Gmail does, or else the last.
func (tv *ThreadView) startAt() int {
	for n, m := range tv.msgs {
		if m.IsUnread() {
			return n
		}
	}
	return len(tv.msgs) - 1
}

// receive shows msgs, the conversation as loaded, scrolled to scroll
// before, and returns where it is scrolled to now, and the IDs of the
// messages that have arrived since it was last loaded, if it was.
// Opened, it starts at startAt; loaded again, the selected message stays
// selected, where it is on screen, even if messages before it come or go.
func (tv *ThreadView) receive(ctx context.Context, msgs []*cmdg.Message,
	scroll int) (int, []string) {
	first := len(tv.msgs) == 0
	selID, ofs := "", 0
	if !first && tv.sel < len(tv.starts) && tv.sel < len(tv.msgs) {
		selID = tv.msgs[tv.sel].ID
		ofs = tv.starts[tv.sel] - scroll
	}
	arrived := newArrivals(tv.msgs, msgs)
	tv.arrived.add(arrived...)
	tv.msgs = msgs
	tv.build(ctx, tv.ov.screen.Width)
	if first {
		tv.sel = tv.startAt()
		if tv.sel >= 0 && tv.sel < len(tv.starts) {
			scroll = tv.starts[tv.sel]
		}
	}
	for n, m := range tv.msgs {
		if m.ID == selID && n < len(tv.starts) {
			tv.sel = n
			scroll = tv.starts[n] - ofs
		}
	}
	return tv.clamp(scroll), arrived
}

// Run runs the conversation view event loop.
func (tv *ThreadView) Run(ctx context.Context) (*MessageViewOp, error) {
	log.Infof("Running ThreadView")
	ov := tv.ov
	scroll := 0

	// Report focus, which says whether a message that has arrived is
	// being read, and whether to ring the bell for it.
	fmt.Print(display.FocusReportOn)
	defer fmt.Print(display.FocusReportOff)
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go tv.watch(wctx)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	// viewed records what is on screen for the messages that have
	// arrived, marks those read that have been, and returns whether
	// any were.
	viewed := func() bool {
		if len(tv.arrived.list) == 0 {
			return false
		}
		read := tv.arrived.update(time.Now(), tv.spans(), scroll,
			scroll+tv.contentHeight(), ov.keys.Focused())
		if len(read) == 0 {
			return false
		}
		log.Infof("Arrived messages read: %q", read)
		tv.markArrivalsRead(ctx, read)
		return true
	}
	redraw := func() {
		viewed()
		if err := tv.draw(tv.lines, scroll); err != nil {
			log.Infof("Failed to draw: %v", err)
		}
	}
	// toMessage selects message n, if there is one, and scrolls to
	// its start, or as near as the end of the conversation allows.
	toMessage := func(n int) {
		if n >= 0 && n < len(tv.starts) {
			tv.sel = n
			scroll = tv.clamp(tv.starts[n])
		}
	}
	// rebuild lays the conversation out again, keeping the selected
	// message where it is on screen.
	rebuild := func() {
		ofs := 0
		if tv.sel < len(tv.starts) {
			ofs = tv.starts[tv.sel] - scroll
		}
		tv.build(ctx, ov.screen.Width)
		if tv.sel < len(tv.starts) {
			scroll = tv.clamp(tv.starts[tv.sel] - ofs)
		}
		ov.screen.Clear()
		redraw()
	}
	scrollBy := func(n int) {
		scroll = tv.clamp(scroll + n)
		tv.follow(scroll)
		redraw()
	}

	ov.screen.Printf(0, 0, "Loading…")
	ov.screen.Draw()
	for {
		select {
		case <-ov.keys.Winch():
			log.Infof("ThreadView got WINCH")
			screen, err := display.NewScreen()
			if err != nil {
				return nil, err
			}
			ov.screen = screen
			rebuild()
		case err := <-ov.errors:
			if err != nil {
				showError(ov.screen, ov.keys, err.Error())
				ov.screen.Draw()
			}
			continue
		case msgs := <-tv.loaded:
			var arrived []string
			scroll, arrived = tv.receive(ctx, msgs, scroll)
			tv.markRead(ctx)
			if len(arrived) > 0 {
				log.Infof("Messages arrived: %q", arrived)
				if !ov.keys.Focused() {
					// st sets the window's urgency hint,
					// which dwm shows on its tag.
					fmt.Print("\a")
				}
			}
			ov.screen.Clear()
			redraw()
		case p := <-tv.polled:
			if len(tv.msgs) > 0 &&
				polledNew(p.ids, p.labels, tv.msgs, tv.rep) {
				go tv.load(ctx, false)
			}
			continue
		case <-ov.keys.Focus():
			if viewed() {
				redraw()
			}
		case <-tick.C:
			if !viewed() {
				continue
			}
			redraw()
		case key, ok := <-ov.keys.Chan():
			if !ok {
				log.Errorf("ThreadView: Input channel closed!")
				continue
			}
			key = threadKey(key)

			switch key {
			case input.CtrlR:
				go tv.load(ctx, true)
			case "?", input.F1:
				txt := customize.OpenMessageHelp(
					threadViewHelp)
				if err := help(txt, ov.keys); err != nil {
					log.Infof("help() failed: %v", err)
				}
				redraw()
			case "z":
				if tv.sel < len(tv.msgs) {
					id := tv.msgs[tv.sel].ID
					tv.showQuotes[id] = !tv.showQuotes[id]
				}
				rebuild()
			case "H":
				tv.preferHTML = !tv.preferHTML
				rebuild()
			case nextMessageKey:
				toMessage(tv.sel + 1)
				redraw()
			case prevMessageKey:
				// To the start of the selected message if
				// it is above the screen, or else to the one
				// before.
				if tv.sel < len(tv.starts) &&
					scroll > tv.starts[tv.sel] {
					toMessage(tv.sel)
				} else {
					toMessage(tv.sel - 1)
				}
				redraw()
			case "u", input.Left:
				return nil, nil
			case "q":
				return OpQuit(), nil
			case input.CtrlP:
				return OpPrev(), nil
			case input.CtrlN:
				return OpNext(), nil
			case "n", input.Down:
				ov.screen.UseCache()
				scrollBy(1)
			case "p", input.Up:
				ov.screen.UseCache()
				scrollBy(-1)
			case " ", input.CtrlV, input.PgDown:
				scrollBy(tv.contentHeight())
			case input.Backspace, input.CtrlH, input.PgUp,
				"Meta-v":
				scrollBy(-tv.contentHeight())
			case input.Home, input.XHome:
				scroll = 0
				tv.sel = 0
				redraw()
			case customize.GoBottom:
				scroll = tv.clamp(len(tv.lines))
				tv.sel = len(tv.msgs) - 1
				redraw()
			case customize.HalfPageDown:
				scrollBy(halfPage(tv.contentHeight()))
			case customize.HalfPageUp:
				scrollBy(-halfPage(tv.contentHeight()))
			case "s", input.CtrlS:
				ns, err := ov.incrementalSearch(ctx, tv.lines)
				if err != nil {
					return nil, err
				}
				if ns > 0 {
					scroll = tv.clamp(ns)
					tv.sel = tv.current(ns)
				}
				redraw()
			case "f":
				ov.forward(ctx)
			case "r":
				ov.reply(ctx)
			case "a":
				ov.replyAll(ctx)
			case "t", input.Right:
				tv.attachments(ctx)
				redraw()
			case "\\":
				if err := ov.showRaw(ctx); err != nil {
					ov.errors <- err
				}
				redraw()
			case "e":
				err := tv.modify(ctx, nil,
					[]string{cmdg.Inbox}, true)
				if err == nil {
					return OpRemoveCurrent(nil), nil
				}
				ov.errors <- fmt.Errorf(
					"Failed to archive: %v", err)
			case "d":
				err := tv.modify(ctx, []string{cmdg.Trash},
					[]string{cmdg.Inbox}, true)
				if err == nil {
					return OpRemoveCurrent(nil), nil
				}
				ov.errors <- fmt.Errorf(
					"Failed to delete: %v", err)
			case "U":
				m := tv.newest()
				err := m.AddLabelID(ctx, cmdg.Unread)
				if err == nil {
					tv.tell(m, []string{cmdg.Unread}, nil,
						false)
					return nil, nil
				}
				ov.errors <- fmt.Errorf(
					"Failed to mark unread: %v", err)
			case "*":
				tv.toggleStar(ctx)
				rebuild()
			case "l":
				tv.label(ctx)
				redraw()
			case "L":
				tv.unlabel(ctx)
				redraw()
			default:
				log.Infof("Unknown key: %q", key)
			}
		}
		ov.screen.Draw()
	}
}

// redraw draws lines scrolled to scroll, with drawLines if it is set.
func (ov *OpenMessageView) redraw(lines []string, scroll int) error {
	if ov.drawLines != nil {
		return ov.drawLines(lines, scroll)
	}
	return ov.Draw(lines, scroll)
}
