package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
	"github.com/ThomasHabets/cmdg/pkg/dialog"
	"github.com/ThomasHabets/cmdg/pkg/display"
	"github.com/ThomasHabets/cmdg/pkg/input"

	"github.com/ThomasHabets/cmdg/internal/customize"
)

const (
	scrollLimit = 5

	messageListViewHelp = `?, F1              — Help
enter, →           — Open message, in a new window
space, x           — Mark message and advance
X                  — Mark message and step up
u                  — Unmark all messages
e                  — Archive marked messages
d                  — Move marked messages to trash
I                  — Mark marked mails as read
l                  — Label marked messages
L                  — Unlabel marked messages
*                  — Toggle starred on highlighted message
c                  — Compose new message, in a new window
C                  — Continue message from draft, in a new window
N, n, ^N, j, Down  — Next message
P, p, ^P, k, Up    — Previous message
r, ^R              — Reload current view
g                  — Go to label
1                  — Go to inbox
U                  — Mark marked mails as unread
s, ^s              — Search
q                  — Quit
^L                 — Refresh screen

Press [enter] to exit
`
)

var (
	//messageListReloadTime          = time.Minute
	messageListReloadTimeout       = 40 * time.Second
	messageListHistoryCheckTime    = 10 * time.Second
	messageListHistoryCheckTimeout = 10 * time.Second
)

type historyUpdate struct {
	historyID cmdg.HistoryID
	history   []*gmail.History
}

type concurrency struct {
	lock chan struct{}
}

func newConcurrency(i int) *concurrency {
	return &concurrency{
		lock: make(chan struct{}, 1),
	}
}
func (c *concurrency) Take() bool {
	select {
	case c.lock <- struct{}{}:
		return true
	default:
		return false
	}
}
func (c *concurrency) Done() {
	<-c.lock
}

// MessageView is the state for a message view.
type MessageView struct {
	// Static state.
	label string
	query string
	// filters are the regexps that a message's From, Subject or
	// snippet must match, each of them, to be listed. Added by this
	// fork; see filterListHelp.
	filters []*regexp.Regexp
	// minListed is how many messages to list from Gmail before
	// waiting for the cursor to near the end: as many as the view
	// filtered had, so that a filter applies to all of them.
	minListed int

	// Communicate with main thread.
	keys            *input.Input
	errors          chan error
	pageCh          chan listedPage
	messageCh       chan *cmdg.Message
	historyUpdateCh chan historyUpdate
	removeMessage   chan string

	// Only for use by main thread.
	messages  []*cmdg.Message
	pos       int
	historyID cmdg.HistoryID

	// conv groups messages into conversations. Added by this fork.
	conv *conversations
}

// NewMessageView creates a new message view.
func NewMessageView(ctx context.Context, label, q string, in *input.Input) *MessageView {
	return newFilteredView(ctx, label, q, nil, 0, in)
}

// newFilteredView is NewMessageView listing only the messages that match
// every one of filters, and at least minListed of Gmail's results to
// match them against, if there are as many.
func newFilteredView(ctx context.Context, label, q string,
	filters []*regexp.Regexp, minListed int,
	in *input.Input) *MessageView {
	v := &MessageView{
		label:           label,
		filters:         filters,
		minListed:       minListed,
		errors:          make(chan error, 20),
		pageCh:          make(chan listedPage),
		historyUpdateCh: make(chan historyUpdate, 20),
		messageCh:       make(chan *cmdg.Message),
		keys:            in,
		query:           q,
		conv:            newConversations(*conversationsFlag),
	}
	v.conv.redraw = func(m *cmdg.Message) {
		// Given up on once the view is no longer running.
		select {
		case v.messageCh <- m:
		case <-time.After(time.Minute):
		}
	}
	go v.fetchPage(ctx, "", 0)
	return v
}

// returns:
// * true if doing anything. If this is 'false' then don't use other two returns.
// * new list of messages
// * an offset of how much pos should go back by after removal
func (mv *MessageView) applyMarked(ctx context.Context, name string, op func(context.Context, []string) error, marked map[string]bool) (bool, []*cmdg.Message, int) {
	ids, nm, ofs := filterMarked(mv.messages, marked, mv.pos)
	if len(ids) == 0 {
		log.Infof("No marked messages to do do operation %q on", name)
		return false, nil, 0
	}
	ids = mv.conv.expand(ids)
	go func() {
		st := time.Now()
		if err := op(ctx, ids); err != nil {
			mv.errors <- errors.Wrapf(err, "batch operation %q failed", name)
		}
		log.Infof("Batch operation %q on %d messages: %v", name, len(ids), time.Since(st))
	}()
	log.Infof("Batch operation %q on %d messages (in background)", name, len(ids))
	return true, nm, ofs
}

// fetchPage lists the page token names, for the list loaded the gen'th
// time, and sends it on mv.pageCh, or a listedPage without a page if it
// could not be listed.
func (mv *MessageView) fetchPage(ctx context.Context, token string,
	gen int) {
	if token == "" {
		// Only update history on first page.
		hctx, cancel := context.WithTimeout(ctx,
			messageListReloadTimeout)
		hid, err := conn.HistoryID(hctx)
		cancel()
		if err != nil {
			log.Errorf("Failed to get history ID: %v", err)
		} else {
			log.Infof("Initing history ID to %d", hid)
			mv.historyUpdateCh <- historyUpdate{
				historyID: hid,
			}
		}
	}

	log.Infof("Listing messages on label %q query %q with token %q…", mv.label, mv.query, token)
	st := time.Now()
	page, err := mv.listPage(ctx, token)
	if err != nil {
		mv.errors <- err
		mv.pageCh <- listedPage{gen: gen}
		return
	}
	log.Infof("Listing messages took %v", time.Since(st))
	n := len(page.Messages)
	// Filtering needs the headers of every message listed, so they
	// are loaded before the page is shown. Otherwise each is loaded as
	// its row is drawn, as upstream loaded every one listed, which
	// paging through results soon spends Gmail's quota on. Added by
	// this fork.
	if len(mv.filters) > 0 {
		ctx, cancel := context.WithTimeout(ctx,
			messageListReloadTimeout)
		defer cancel()
		if err := page.PreloadSubjects(ctx); err != nil {
			mv.errors <- err
		}
		mv.filterPage(ctx, page)
	}
	mv.pageCh <- listedPage{page, n, gen}
}

// maxListBackoff is how long listing a page, refused by Gmail for its
// rate limits, waits for them in all before giving up. Gmail's quota is
// per minute, so a minute gets past it unless something else keeps
// spending it.
const maxListBackoff = time.Minute

// listPage lists the page token names, waiting and trying again if Gmail
// refuses for its rate limits, as paging through results can spend the
// quota while the address book scan is spending it as well. Each try has
// its own messageListReloadTimeout. Added by this fork.
func (mv *MessageView) listPage(ctx context.Context,
	token string) (*cmdg.Page, error) {
	var waited time.Duration
	for wait := time.Second; ; wait *= 2 {
		tctx, cancel := context.WithTimeout(ctx,
			messageListReloadTimeout)
		page, err := conn.ListMessages(tctx, mv.label, mv.query,
			token)
		cancel()
		if !cmdg.IsRateLimited(err) || waited >= maxListBackoff {
			return page, err
		}
		log.Warningf("Listing rate limited, trying again in %v",
			wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		waited += wait
	}
}

// listedPage is a page of the list, and how many messages Gmail listed in
// it, which is more than the page holds once filtered. gen tells it from
// a page of the list as it was before being loaded again.
type listedPage struct {
	*cmdg.Page
	listed int
	gen    int
}

// filterPage drops from page the messages not matching mv.filters.
func (mv *MessageView) filterPage(ctx context.Context, page *cmdg.Page) {
	var msgs []*cmdg.Message
	var resp []*gmail.Message
	for n, m := range page.Messages {
		if mv.matches(ctx, m) {
			msgs = append(msgs, m)
			if n < len(page.Response.Messages) {
				resp = append(resp, page.Response.Messages[n])
			}
		}
	}
	page.Messages = msgs
	page.Response.Messages = resp
}

// matches reports whether every one of mv.filters matches the From
// header, the subject or the snippet of m. A message whose headers failed
// to load is kept, rather than hidden for a reason the list cannot show.
func (mv *MessageView) matches(ctx context.Context, m *cmdg.Message) bool {
	var fields []string
	for _, get := range []func(context.Context) (string, error){
		func(ctx context.Context) (string, error) {
			return m.GetHeader(ctx, "From")
		},
		m.GetSubject,
		m.GetSnippet,
	} {
		f, err := get(ctx)
		if errors.Cause(err) == cmdg.ErrMissing {
			continue
		}
		if err != nil {
			log.Warningf("Filtering message %s: %v", m.ID, err)
			return true
		}
		fields = append(fields, f)
	}
	for _, re := range mv.filters {
		found := false
		for _, f := range fields {
			if re.MatchString(f) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// filtered asks for a regexp, and returns a view of the messages in this
// one that match it too, of which this one has listed listed. Given none,
// it returns the view without the last filter, as the views are not a
// stack that q could return along. It returns nil if there is no view to
// change to.
func (mv *MessageView) filtered(ctx context.Context,
	listed int) *MessageView {
	f, err := dialog.Entry("Filter> ", mv.keys)
	if err == dialog.ErrAborted {
		return nil
	} else if err != nil {
		mv.errors <- errors.Wrapf(err, "Getting filter")
		return nil
	}
	// Copied, so that the views do not share an array to append to.
	fs := append([]*regexp.Regexp{}, mv.filters...)
	if f != "" {
		fs = append(fs, compileFilter(f))
	} else if len(fs) > 0 {
		fs = fs[:len(fs)-1]
	} else {
		return nil
	}
	return newFilteredView(ctx, mv.label, mv.query, fs, listed, mv.keys)
}

// filterStatus returns the filters for the status line, if any.
func (mv *MessageView) filterStatus() string {
	if len(mv.filters) == 0 {
		return ""
	}
	var fs []string
	for _, re := range mv.filters {
		fs = append(fs, strings.TrimPrefix(re.String(), filterPrefix))
	}
	return fmt.Sprintf("Filter: %s  ", strings.Join(fs, " & "))
}

// filterPrefix makes a filter case insensitive, like the search in a
// message.
const filterPrefix = "(?i)"

// compileFilter compiles the regexp s, after filterPrefix. Something not
// a regexp, such as "a(", is searched for as it is written.
func compileFilter(s string) *regexp.Regexp {
	if re, err := regexp.Compile(filterPrefix + s); err == nil {
		return re
	}
	return regexp.MustCompile(filterPrefix + regexp.QuoteMeta(s))
}

// filterListHelp is the help for filtering search results, which this
// fork adds.
const filterListHelp = `s, ^s              — Filter search results by regexp
`

// MessageViewOp is an operation to perform as the message closes.
type MessageViewOp struct {
	fun         func(*MessageView)
	quit        bool
	nextMessage bool
	prevMessage bool

	next *MessageViewOp
}

// Do does the op.
func (op *MessageViewOp) Do(view *MessageView) {
	if op == nil {
		return
	}
	if op.fun != nil {
		op.fun(view)
	}
	if op.next != nil {
		op.next.Do(view)
	}
}

// IsQuit returns if op is `quit`.
func (op *MessageViewOp) IsQuit(view *MessageView) bool {
	if op == nil {
		return false
	}
	if op.quit {
		return true
	}
	return op.next.IsQuit(view)
}

// IsNext returns if op is `next`.
func (op *MessageViewOp) IsNext(view *MessageView) bool {
	if op == nil {
		return false
	}
	if op.nextMessage {
		return true
	}
	return op.next.IsNext(view)
}

// IsPrev returns if op is `prev`
func (op *MessageViewOp) IsPrev(view *MessageView) bool {
	if op == nil {
		return false
	}
	if op.prevMessage {
		return true
	}
	return op.next.IsPrev(view)
}

// OpRemoveCurrent creates an op to remove current message from a list.
func OpRemoveCurrent(next *MessageViewOp) *MessageViewOp {
	return &MessageViewOp{
		fun: func(view *MessageView) {
			// TODO
			view.messages = append(view.messages[:view.pos], view.messages[view.pos+1:]...)
			if view.pos >= len(view.messages) && view.pos != 0 {
				view.pos--
			}
		},
		next: next,
	}
}

// OpQuit creates an op to quit.
func OpQuit() *MessageViewOp {
	return &MessageViewOp{
		quit: true,
	}
}

// OpPrev creates an op to go to prev message.
func OpPrev() *MessageViewOp {
	return &MessageViewOp{
		prevMessage: true,
	}
}

// OpNext creates an op to go to the next message.
func OpNext() *MessageViewOp {
	return &MessageViewOp{
		nextMessage: true,
	}
}

// filterMarked takes:
// * slice of messages
// * a set of marked message IDs
// * current position
// And returns the state if marked messages are removed
// * ids of the messages removed
// * slice of messages remaining after removal
// * an offset of how much pos should go back by after removal
//
// The offset is returned as an offset because it's used both for
// setting the new position and for adjusting current scroll position.
func filterMarked(msgs []*cmdg.Message, marked map[string]bool, pos int) ([]string, []*cmdg.Message, int) {
	var ids []string
	ms := []*cmdg.Message{}
	ofs := 0
	for n, msg := range msgs {
		if marked[msg.ID] {
			ids = append(ids, msg.ID)
			if n < pos {
				ofs++
			}
		} else {
			ms = append(ms, msg)
		}
	}
	return ids, ms, ofs
}

func filterMessage(msgs []*cmdg.Message, id string, pos int) ([]*cmdg.Message, int) {
	var ret []*cmdg.Message

	for n, msg := range msgs {
		if msg.ID == id {
			if n < pos {
				pos--
			}
			continue
		}
		ret = append(ret, msg)
	}
	return ret, pos
}

func (mv *MessageView) historyCheck(ctx context.Context) error {
	hists, hid, err := conn.History(ctx, mv.historyID, mv.label)
	if err != nil {
		return errors.Wrapf(err, "getting history since %d", mv.historyID)
	}
	if len(hists) == 0 {
		log.Infof("No history since last check")
		return nil
	}

	// The GMail API returns false positives if a new message
	// affects *any thread* that is in the current label, even if
	// the message itself doesn't have the label.
	// This was closed by Google as working as intended. :-(
	//
	// https://issuetracker.google.com/issues/137671760
	//
	// So we'll need to get the messages' list of labels before
	// sending them on to the list view.
	var wg sync.WaitGroup
	for hi := range hists {
		hi := hi
		for mi := range hists[hi].MessagesAdded {
			mi := mi
			if len(hists[hi].MessagesAdded[mi].Message.LabelIds) > 0 {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				m := cmdg.NewMessage(conn, hists[hi].MessagesAdded[mi].Message.Id)
				// Load labels.
				ls, err := m.GetLabels(ctx, true)
				if err != nil {
					log.Errorf("Failed to load labels for history entry: %v", err)
					return
				}
				for _, l := range ls {
					hists[hi].MessagesAdded[mi].Message.LabelIds = append(hists[hi].MessagesAdded[mi].Message.LabelIds, l.ID)
				}
			}()
		}
	}
	wg.Wait()

	mv.historyUpdateCh <- historyUpdate{
		historyID: hid,
		history:   hists,
	}
	return nil
}

// prependRow adds msg to the list as its first row, keeping the cursor on
// the row it was on, which is one further down now. Scrolled to the top,
// the new row shows, and the rows move down, the list scrolling only if
// the cursor would leave the screen, height rows high; scrolled further,
// the rows stay where they are on screen. Added by this fork: upstream
// left mv.pos as it was, so that the cursor moved to the row above.
func (mv *MessageView) prependRow(msg *cmdg.Message, scroll *int,
	height int) {
	if len(mv.messages) > 0 {
		mv.pos++
		if *scroll > 0 || mv.pos-*scroll >= height {
			*scroll++
		}
	}
	mv.messages = append([]*cmdg.Message{msg}, mv.messages...)
}

// Run runs the messagelist view.
// compose composes a new message, in a new window if there can be one.
func (mv *MessageView) compose(ctx context.Context) {
	composeSomewhere("-compose", "composing new message", func() error {
		return composeNew(ctx, conn, conn.Contacts(), nil, mv.keys)
	}, mv.errors, mv.errors)
}

// openInWindow opens the current message in a new window, with the IDs of
// the listed messages for ^N and ^P.
func (mv *MessageView) openInWindow() {
	msg := mv.messages[mv.pos]
	ids := make([]string, len(mv.messages))
	for n, m := range mv.messages {
		ids[n] = m.ID
	}
	if err := startWindow(conn, mv.conv.readFlag()+"="+msg.ID, ids,
		windowEventSocket(), mv.errors); err != nil {
		mv.errors <- errors.Wrap(err, "Opening message window")
		return
	}
	// The window marks it read, which the next history check would
	// show, but showing it now is less confusing.
	msg.RemoveLabelIDLocal(cmdg.Unread)
	for _, m := range mv.conv.members[msg.ID] {
		m.RemoveLabelIDLocal(cmdg.Unread)
	}
}

// applyWindowEvent applies to the list the change a message window made,
// as the history check would, and as it would have been had the message
// been opened in this terminal. It returns the index in the list of the
// message removed, or -1 if none was.
func (mv *MessageView) applyWindowEvent(ev windowEvent) int {
	ind := -1
	for n, m := range mv.messages {
		if m.ID == ev.ID {
			ind = n
			break
		}
	}
	if ind < 0 {
		log.Infof("Window event for %q, which is not listed", ev.ID)
		return -1
	}
	msg := mv.messages[ind]
	for _, l := range ev.AddLabels {
		msg.AddLabelIDLocal(l)
	}
	drop := ev.Drop
	for _, l := range ev.RemoveLabels {
		msg.RemoveLabelIDLocal(l)
		if l == mv.label {
			drop = true
		}
	}
	if !drop {
		return -1
	}
	mv.messages = append(mv.messages[:ind], mv.messages[ind+1:]...)
	if ind < mv.pos || (mv.pos >= len(mv.messages) && mv.pos > 0) {
		mv.pos--
	}
	return ind
}

// continueDraft continues a draft, in a new window if there can be one.
func (mv *MessageView) continueDraft(ctx context.Context) {
	composeSomewhere("-continue_draft", "continuing draft", func() error {
		return continueDraft(ctx, conn, mv.keys)
	}, mv.errors, mv.errors)
}

func (mv *MessageView) Run(ctx context.Context) error {
	log.Infof("Running MessageView")
	// TODO: defer a sync.WaitGroup.Wait() waiting on all goroutines spawned.
	// fetching is whether a page is being listed, and nextToken
	// names the page after the last one listed, if any. Added by this
	// fork: upstream lists pages only until the screen is full.
	fetching := true
	nextToken := ""
	// gen counts the times the list has been loaded again, so that
	// a page listed before is not added to it.
	gen := 0
	var contentHeight int
	var pages []*cmdg.Page
	// gmailListed is how many messages Gmail has listed in pages.
	gmailListed := 0
	messagePos := map[string]int{}
	marked := map[string]bool{}
	var scroll int
	var screen *display.Screen

	initScreen := func() error {
		var err error
		screen, err = display.NewScreen()
		if err != nil {
			return err
		}
		contentHeight = screen.Height - 2
		scroll = 0 // TODO: only scroll back if we need to.
		return nil
	}
	if err := initScreen(); err != nil {
		return err
	}
	defer func() {
		screen.Clear()
		screen.Draw()
	}()

	mkMessagePos := func() {
		mv.regroup(marked, &scroll)
		messagePos = map[string]int{}
		for n, m := range mv.messages {
			messagePos[m.ID] = n
		}
	}
	empty := func() {
		screen.Printf(0, 0, "Loading…")
		screen.Draw()
		pages = nil
		gmailListed = 0
		nextToken = ""
		mv.messages = nil
		mkMessagePos()
		mv.pos = 0
		scroll = 0
	}
	empty()
	// reload loads the list again from the first page.
	reload := func() {
		empty()
		screen.Clear()
		gen++
		fetching = true
		go mv.fetchPage(ctx, "", gen)
	}
	// loadMore lists the next page once the cursor is within a
	// screen of the end of the list.
	loadMore := func() {
		if nextToken == "" || fetching ||
			len(mv.messages)-mv.pos > contentHeight {
			return
		}
		fetching = true
		go mv.fetchPage(ctx, nextToken, gen)
	}

	drawMessage := func(cur int) error {
		s := "Loading…"
		if cur >= len(mv.messages) {
			return fmt.Errorf("trying to draw message %d with len %d", cur, len(mv.messages))
		}
		curmsg := mv.messages[cur]

		prefix := " "
		reset := display.Reset
		if cur == mv.pos {
			reset = display.Reverse
			prefix = "*"
		}

		if curmsg.HasData(cmdg.LevelMetadata) {
			subj, err := curmsg.GetSubject(ctx)
			if errors.Cause(err) == cmdg.ErrMissing || subj == "" {
				subj = "(No subject)"
			} else if err != nil {
				return err
			}
			tm, err := curmsg.GetTimeFmt(ctx)
			if err != nil {
				log.Infof("Failed to parse mail date: %v", err)
				tm = "???"
			}
			from, err := curmsg.GetFrom(ctx)
			if err != nil {
				return err
			}
			colors, fullColors, err := curmsg.GetLabelColors(ctx, mv.label)
			if err != nil {
				return err
			}
			if len(colors) > 0 {
				colors = " | " + colors
				fullColors = " | " + fullColors
			}
			from = mv.conv.fromColumn(curmsg, from, 20)
			s = fmt.Sprintf("%[1]*.[1]*[2]s | %[3]s | %[4]s",
				6, tm,
				from, subj)
			if display.StringWidth(s)+display.StringWidth(fullColors) < screen.Width {
				s += fullColors
			} else {
				s += colors
			}
			s += reset
		} else {
			go func(cur int) {
				if err := curmsg.Preload(ctx, cmdg.LevelMetadata); err != nil {
					log.Warningf("Failed to load metadata for email ID %s: %v", curmsg.ID, err)
					if e, ok := errors.Cause(err).(*googleapi.Error); ok {
						log.Warningf("Failing to load was googleapi error %+v", e)
						if e.Code == 404 {
							mv.removeMessage <- curmsg.ID
						}
					}
				} else {
					mv.messageCh <- curmsg
				}
			}(cur)
		}

		mv.conv.loadMembers(ctx, curmsg)

		if marked[curmsg.ID] {
			prefix += "X"
		} else {
			prefix += " "
		}

		if mv.conv.unread(curmsg) {
			prefix = display.Bold + prefix + ">"
		} else {
			prefix += " "
		}

		star := " "
		if curmsg.HasLabel(cmdg.Starred) {
			star = "*"
			prefix = display.Yellow + prefix
		}

		screen.Printlnf(cur-scroll, "%s%s%s%s", reset, prefix, star, s)
		return nil
	}

	timer := time.NewTicker(messageListHistoryCheckTime)
	defer timer.Stop()
	historyConcurrency := newConcurrency(1)

	prev := func() bool {
		if mv.pos <= 0 {
			return false
		}
		mv.pos--
		if scroll > 0 && mv.pos < scroll+scrollLimit {
			scroll--
		}
		return true
	}
	next := func() bool {
		loadMore()
		if mv.messages == nil {
			return false
		}
		if mv.pos >= len(mv.messages)-1 {
			return false
		}
		if mv.pos-scroll > contentHeight-scrollLimit {
			scroll++
		}
		mv.pos++
		return true
	}
	for {
		status := ""
		select {
		case histUpdate := <-mv.historyUpdateCh:
			log.Infof("Got history update: %+v", histUpdate)
			if histUpdate.historyID < mv.historyID {
				log.Warningf("Got out of order history entry %d < %d", histUpdate.historyID, mv.historyID)
			} else if histUpdate.historyID == mv.historyID {
				log.Infof("Got duplicate history update %d", mv.historyID)
			} else {
				mv.historyID = histUpdate.historyID
				mv.conv.noteHistory(histUpdate.history,
					mv.label)
				for _, hist := range histUpdate.history {
					log.Infof("History entry: %d add, %d delete, %d labeladd, %d labeldelete", len(hist.MessagesAdded), len(hist.MessagesDeleted), len(hist.LabelsAdded), len(hist.LabelsRemoved))
					for _, m := range hist.MessagesDeleted {
						ind, found := messagePos[m.Message.Id]
						if found {
							log.Infof("Deleting message from in accordance with history")
							mv.messages = append(mv.messages[:ind], mv.messages[ind+1:]...)
							if ind < mv.pos {
								mv.pos--
							}
						}
					}

					for _, ladd := range hist.LabelsAdded {
						// Messages moved into this label (and other labels).
						if msgn, found := messagePos[ladd.Message.Id]; found {
							msg := mv.messages[msgn]
							for _, l := range ladd.LabelIds {
								log.Infof("Adding label %q", l)
								msg.AddLabelIDLocal(l)
							}
						} else {
							// New message for this view.
							this := false
							for _, l := range ladd.LabelIds {
								if l == mv.label {
									this = true
									break
								}
							}
							if this {
								// Confirmed. This is a new message.
								log.Infof("History says %q was moved to current label %q", ladd.Message.Id, mv.label)
								nm := cmdg.NewMessage(conn, ladd.Message.Id)
								// TODO: add it in the right place, not the top.
								mv.prependRow(nm, &scroll, contentHeight)
								mkMessagePos()
							}
						}
					}

					for _, ma := range hist.MessagesAdded {
						// New messages… also in this view.
						if _, found := messagePos[ma.Message.Id]; !found {
							// Double-check that the message has the current label.
							// If there are no labels then err on the side of showing the message.
							//
							// That probably the right behaviour since it should only happen for
							// no-label searches getting new results.
							addme := true
							hasData := false
							if len(ma.Message.LabelIds) > 0 {
								addme = false
								hasData = true
								for _, l := range ma.Message.LabelIds {
									if l == mv.label {
										addme = true
										break
									}
								}
							}
							if addme {
								log.Infof("Adding message from history")
								var nm *cmdg.Message
								if hasData {
									nm = cmdg.NewMessageWithResponse(conn, ma.Message.Id, ma.Message, cmdg.LevelMinimal)
								} else {
									nm = cmdg.NewMessage(conn, ma.Message.Id)
								}
								// TODO: add it in the right place, not the top.
								mv.prependRow(nm, &scroll, contentHeight)
								mkMessagePos()
							} else {
								log.Infof("Skipped adding message because history returned false positive")
							}
						}
					}
					for _, lrm := range hist.LabelsRemoved {
						ind, found := messagePos[lrm.Message.Id]
						if found {
							msg := mv.messages[ind]
							this := false
							for _, l := range lrm.LabelIds {
								for _, el := range msg.LocalLabels() {
									if l == el {
										msg.RemoveLabelIDLocal(l)
									}
								}
								if l == mv.label {
									this = true
								}
							}
							if this {
								log.Infof("… message %s gone from this view", lrm.Message.Id)
								mv.messages = append(mv.messages[:ind], mv.messages[ind+1:]...)
								if ind < mv.pos {
									mv.pos--
								}
								mkMessagePos()
							}
						}
					}
				}
			}

		case ev := <-windowEvents:
			mv.conv.applyWindowEvent(ev, mv.label)
			if ind := mv.applyWindowEvent(ev); ind >= 0 {
				if ind < scroll {
					scroll--
				}
				mkMessagePos()
			}

		case <-timer.C: // Check history every now and then.
			if mv.label != "" {
				if historyConcurrency.Take() {
					st := time.Now()
					go func() {
						defer historyConcurrency.Done()
						defer func() {
							log.Infof("History check took %v", time.Since(st))
						}()
						ctx, cancel := context.WithTimeout(ctx, messageListHistoryCheckTimeout)
						defer cancel()
						if err := mv.historyCheck(ctx); err != nil {
							log.Errorf("Error getting history: %s", err)
						}
					}()
				} else {
					log.Infof("Not history checking because one is already running")
				}
			} else {
				log.Infof("Not checking history because not in a label")
			}
			if false {
				// TODO: don't reset pos and scroll
				log.Infof("Timed reload")
				reload()
			}

		case <-mv.keys.Winch():
			log.Infof("MessageListView got WINCH!")
			if err := initScreen(); err != nil {
				// Screen failed to init. Yeah it's time to bail.
				return err
			}
		case err := <-mv.errors:
			showError(screen, mv.keys, err.Error())
			screen.Draw()
			continue
		case m := <-mv.messageCh:
			cur, ok := messagePos[m.ID]
			if !ok || cur < scroll || cur-scroll >= contentHeight {
				// Gone from the list or the screen since.
				// Added by this fork, as conversation counts
				// arrive late.
				continue
			}
			if err := drawMessage(cur); err != nil {
				mv.errors <- errors.Wrapf(err, "Drawing message")
			}
			screen.Draw() // TODO: avoid redrawing whole screen.
			continue
		case p := <-mv.pageCh:
			log.Printf("MessageListView: Got page!")
			if p.gen != gen {
				log.Infof("Dropping a page from before")
				continue
			}
			fetching = false
			if p.Page == nil {
				// Failed, and shown as an error. nextToken
				// is kept, so that moving down tries again.
				break
			}
			pages = append(pages, p.Page)
			gmailListed += p.listed
			mv.conv.note(p.Response.Messages...)
			mv.messages = append(mv.messages, p.Messages...)
			mkMessagePos()
			nextToken = p.Response.NextPageToken
			// A filtered list fills the screen only from what
			// Gmail lists to fill it unfiltered, rather than
			// paging through every result to find matches.
			fill := len(mv.messages) < contentHeight &&
				(len(mv.filters) == 0 ||
					gmailListed < contentHeight)
			if nextToken == "" {
				log.Infof("All pages loaded")
			} else if fill || gmailListed < mv.minListed {
				fetching = true
				go mv.fetchPage(ctx, nextToken, gen)
			} else {
				log.Infof("Enough pages: %d rows of %d",
					len(mv.messages), gmailListed)
			}
			if len(mv.messages) == 0 && !fetching {
				// Not drawn below, as mv.messages is nil.
				screen.Printlnf(0, "<empty>")
			}

		case id := <-mv.removeMessage:
			mv.messages, mv.pos = filterMessage(mv.messages, id, mv.pos)
			mkMessagePos()

		case key, ok := <-mv.keys.Chan():
			if !ok {
				log.Errorf("MessageList: Input channel closed!")
				continue
			}
			log.Debugf("MessageListView got key %q", key)
			// Local rebindings, applied here rather than in
			// the switch below so that the switch stays as
			// upstream wrote it. See internal/customize.
			key = customize.MessageListKey(key)
			switch key {
			case "?", input.F1:
				txt := customize.MessageListHelp(addHelp(
					messageListViewHelp,
					conversationListHelp+filterListHelp))
				if err := help(txt, mv.keys); err != nil {
					log.Infof("help() failed: %v", err)
				}
			case input.Enter, input.Right:
				if len(mv.messages) == 0 {
					// Let's assume we've never gotten to the state where mv.pos >= len(mv.messages)
					break
				}
				if mv.pos >= len(mv.messages) {
					break
				}
				if useWindows() {
					mv.openInWindow()
					break
				}
				for {
					vo, err := openViewer(ctx,
						mv.messages[mv.pos], mv.keys,
						mv.conv.on, mv.errors, nil)
					if err != nil {
						mv.errors <- errors.Wrapf(err, "Opening message")
					} else {
						op, err := vo.Run(ctx)
						if err != nil {
							mv.errors <- errors.Wrapf(err, "Running OpenMessageView")
						}
						op.Do(mv)
						if op.IsQuit(mv) {
							return nil
						}
						if op.IsPrev(mv) {
							if mv.pos > 0 {
								mv.pos--
								if scroll > 0 {
									scroll--
								}
							}
							continue
						}
						if op.IsNext(mv) {
							if mv.pos < len(mv.messages)-1 {
								mv.pos++
								if mv.pos-scroll > contentHeight-scrollLimit {
									scroll++
								}
							}
							continue
						}
						mkMessagePos() // op.Do() could have changed the message positions around.
					}
					break
				}
			case input.CtrlL:
				if err := initScreen(); err != nil {
					// Screen failed to init. Yeah it's time to bail.
					return err
				}
			case "e":
				idch := make(chan []string)
				ok, nm, ofs := mv.applyMarked(ctx, "archive", func(ctx context.Context, ids []string) error {
					idch <- ids
					return conn.BatchArchive(ctx, ids)
				}, marked)
				if !ok {
					break
				}
				for _, id := range <-idch {
					m := listed(id)
					m.RemoveLabelIDLocal(cmdg.Inbox)
				}
				if mv.label == cmdg.Inbox {
					mv.pos -= ofs
					scroll -= ofs
					if scroll < 0 {
						scroll = 0
					}
					mv.messages = nm
					marked = map[string]bool{}
					mkMessagePos()
				}
			case "I": // Mark read.
				idch := make(chan []string)
				ok, _, _ := mv.applyMarked(ctx, "mark-read", func(ctx context.Context, ids []string) error {
					idch <- ids
					return conn.BatchUnlabel(ctx, ids, cmdg.Unread)
				}, marked)
				if !ok {
					break
				}
				for _, id := range <-idch {
					m := listed(id)
					m.RemoveLabelIDLocal(cmdg.Unread)
				}
			case "U": // Mark unread.
				idch := make(chan []string)
				ok, _, _ := mv.applyMarked(ctx, "mark-unread", func(ctx context.Context, ids []string) error {
					idch <- ids
					return conn.BatchLabel(ctx, ids, cmdg.Unread)
				}, marked)
				if !ok {
					break
				}
				for _, id := range <-idch {
					m := listed(id)
					m.AddLabelIDLocal(cmdg.Unread)
				}
			case "d":
				ok, nm, ofs := mv.applyMarked(ctx, "delete", conn.BatchTrash, marked)
				if !ok {
					break
				}
				mv.pos -= ofs
				scroll -= ofs
				if scroll < 0 {
					scroll = 0
				}
				mv.messages = nm
				marked = map[string]bool{}
				mkMessagePos()

			case "*":
				if mv.pos >= len(mv.messages) {
					break
				}
				// TODO: Because it's a toggle this is not suitable for batch operation.
				curmsg := mv.messages[mv.pos]
				f := curmsg.AddLabelID
				f2 := curmsg.AddLabelIDLocal

				verb := "Adding"
				if curmsg.HasLabel(cmdg.Starred) {
					f = curmsg.RemoveLabelID
					f2 = curmsg.RemoveLabelIDLocal
					verb = "Removing"
				}
				f2(cmdg.Starred)
				go func() {
					if err := f(ctx, cmdg.Starred); err != nil {
						mv.errors <- errors.Wrapf(err, "%s STARRED label", verb)
					}
				}()
			case "l":
				// TODO: can this be partially merged with 'L' code?
				ids, _, _ := filterMarked(mv.messages, marked, mv.pos)
				ids = mv.conv.expand(ids)
				if len(ids) != 0 {
					var opts []*dialog.Option
					for _, l := range conn.Labels() {
						opts = append(opts, &dialog.Option{
							Key:   l.ID,
							Label: l.Label,
						})
					}
					label, err := dialog.Selection(opts, "Label> ", false, mv.keys)
					if errors.Cause(err) == dialog.ErrAborted {
						// No-op.
					} else if err != nil {
						mv.errors <- errors.Wrapf(err, "Selecting label")
					} else {
						for _, id := range ids {
							m := listed(id)
							m.AddLabelIDLocal(label.Key)
						}
						log.Infof("Batch labelling %q/%q %d messages in the background…", label.Key, label.Label, len(ids))
						go func() {
							st := time.Now()
							if err := conn.BatchLabel(ctx, ids, label.Key); err != nil {
								mv.errors <- errors.Wrapf(err, "Batch labelling")
							} else {
								log.Infof("Batch labelled %d: %v", len(ids), time.Since(st))
							}
						}()
					}
				}
			case "L":
				ids, _, _ := filterMarked(mv.messages, marked, mv.pos)
				if len(ids) != 0 {
					var opts []*dialog.Option
				outer:
					for _, l := range conn.Labels() {
						for _, m := range ids {
							if !mv.messages[messagePos[m]].HasLabel(l.ID) {
								log.Warningf("Unknown label ID %q", l.ID)
								continue outer
							}
						}
						opts = append(opts, &dialog.Option{
							Key:   l.ID,
							Label: l.Label,
						})
					}
					if len(opts) > 0 {
						label, err := dialog.Selection(opts, "Label> ", false, mv.keys)
						if errors.Cause(err) == dialog.ErrAborted {
							// No-op.
						} else if err != nil {
							mv.errors <- errors.Wrapf(err, "Selecting label")
						} else {
							ids = mv.conv.
								expand(ids)
							for _, id := range ids {
								m := listed(id)
								m.RemoveLabelIDLocal(label.Key)
							}
							log.Infof("Batch unlabelling %q/%q from %d messages in the background…", label.Key, label.Label, len(ids))
							go func() {
								st := time.Now()
								if err := conn.BatchUnlabel(ctx, ids, label.Key); err != nil {
									mv.errors <- errors.Wrapf(err, "Batch labelling")
								} else {
									log.Infof("Batch unlabelled %d: %v", len(ids), time.Since(st))
								}
							}()
						}
					}
				}
			case "c":
				mv.compose(ctx)
			case "C":
				mv.continueDraft(ctx)
			case input.Home, input.XHome:
				mv.pos = 0
				scroll = 0
			case "x", " ":
				if mv.pos < len(mv.messages) {
					marked[mv.messages[mv.pos].ID] = !marked[mv.messages[mv.pos].ID]
					next()
				}
			case "X":
				if mv.pos < len(mv.messages) {
					marked[mv.messages[mv.pos].ID] = !marked[mv.messages[mv.pos].ID]
					prev()
				}
			case "u":
				marked = map[string]bool{}
			// UseCache only once it is certain to be drawn
			// next: left set, it would apply to the next draw
			// whatever came between, such as an opened message,
			// and leave that showing.
			case "N", "n", "j", input.CtrlN, input.Down:
				if !next() {
					// If already on last one, don't redraw.
					continue
				}
				screen.UseCache()
			case "P", "p", "k", input.CtrlP, input.Up:
				if !prev() {
					// If already on first one, don't redraw.
					continue
				}
				screen.UseCache()
			case "r", input.CtrlR:
				reload()
			case "g":
				var opts []*dialog.Option
				for _, l := range conn.Labels() {
					if strings.HasPrefix(l.ID, "CATEGORY_") {
						continue
					}
					if l.ID == "IMPORTANT" {
						continue
					}
					opts = append(opts, &dialog.Option{
						Key:   l.ID,
						Label: l.LabelString(),
					})
				}
				label, err := dialog.Selection(opts, "Label> ", false, mv.keys)
				if errors.Cause(err) == dialog.ErrAborted {
					// No-op.
				} else if err != nil {
					mv.errors <- errors.Wrapf(err, "Selecting label")
				} else {
					nv := NewMessageView(ctx, label.Key, "", mv.keys)
					// TODO: not optimal, since it adds a
					// stack frame on every navigation.
					return nv.Run(ctx)
				}
			case "1":
				// TODO: not optimal, since it adds a
				// stack frame on every navigation.
				return NewMessageView(ctx, cmdg.Inbox, "", mv.keys).Run(ctx)
			case "s", input.CtrlS:
				// Added by this fork: in search results,
				// search them instead of Gmail.
				if mv.query != "" {
					nv := mv.filtered(ctx, gmailListed)
					if nv != nil {
						return nv.Run(ctx)
					}
					break
				}
				q, err := dialog.Entry("Query> ", mv.keys)
				if err == dialog.ErrAborted {
					// That's fine.
				} else if err != nil {
					mv.errors <- errors.Wrapf(err, "Getting query")
				} else {
					nv := NewMessageView(ctx, "", q, mv.keys)
					// TODO: not optimal, since it adds a
					// stack frame on every navigation.
					return nv.Run(ctx)
				}
			case "q":
				return nil
			// Added by this fork; see the matching case in
			// view_openmessage.go. Only the messages loaded so
			// far can be jumped to: the list pages in lazily,
			// and going there loads the next page.
			case customize.GoBottom:
				if n := len(mv.messages); n > 0 {
					mv.pos = n - 1
					loadMore()
					scroll = mv.pos - contentHeight + 1
					if scroll < 0 {
						scroll = 0
					}
				}
			// next() and prev() rather than arithmetic, so
			// that upstream keeps owning the scroll
			// bookkeeping and the ends of the list.
			case customize.HalfPageDown:
				for n := halfPage(contentHeight); n > 0; n-- {
					if !next() {
						break
					}
				}
			case customize.HalfPageUp:
				for n := halfPage(contentHeight); n > 0; n-- {
					if !prev() {
						break
					}
				}
			case customize.PageDown:
				for n := fullPage(contentHeight); n > 0; n-- {
					if !next() {
						break
					}
				}
			case customize.PageUp:
				for n := fullPage(contentHeight); n > 0; n-- {
					if !prev() {
						break
					}
				}
			// Added by this fork; see conversations.go. Grouping
			// needs nothing fetched, but ungrouping reloads, as
			// a row's messages belong in date order among the
			// other rows'.
			case "T":
				*conversationsFlag = !*conversationsFlag
				mv.conv.on = *conversationsFlag
				if mv.conv.on {
					mkMessagePos()
				} else {
					reload()
				}
			default:
				log.Infof("MessageListView got unknown key %q %v", key, []byte(key))
			}
		}
		if mv.messages != nil {
			// Draw to buffer.
			st := time.Now()
			for n := 0; n < contentHeight; n++ {
				cur := n + scroll
				if cur >= len(mv.messages) {
					screen.Printlnf(n, "")
					continue
				}

				if err := drawMessage(cur); err != nil {
					mv.errors <- err
				}

				if time.Since(st) > 10*time.Millisecond {
					screen.Draw()
				}
			}
			if len(mv.messages) == 0 {
				screen.Printlnf(0, "<empty>")
			}
			log.Debugf("Print took %v", time.Since(st))
		}
		// Print status.
		status += mv.filterStatus()
		if fetching {
			status += display.Color(50) + "Loading…"
		} else if nextToken != "" {
			status += display.Color(50) +
				"More: move down past the end to load"
		}
		screen.Printlnf(screen.Height-2, "%s", strings.Repeat("—", screen.Width))
		screen.Printlnf(screen.Height-1, "%s", status)

		// Draw.
		st := time.Now()
		screen.Draw()
		log.Debugf("Draw took %v", time.Since(st))
	}
}
