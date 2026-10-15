package main

// Messages arriving in an open conversation, added by this fork. The
// conversation view checks its thread every threadPollTime, shows a
// message that has arrived, tells of it on the bottom line until it has
// been read, and rings the terminal's bell if its window does not have the
// focus, which st turns into the window's urgency hint, and dwm into a
// highlighted tag.

import (
	"fmt"
	"strings"
	"time"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
)

// threadPollTime is how often an open conversation checks its thread for
// new messages, as often as the message list checks its label.
var threadPollTime = messageListHistoryCheckTime

const (
	// arrivalReadTime is how long a message that has arrived must have
	// been viewed whole for it to be read.
	arrivalReadTime = 10 * time.Second
)

// arrival is a message that arrived while its conversation was open.
type arrival struct {
	id string
	// seen holds, for each line of the message, whether it has been on
	// screen while the window had the focus.
	seen []bool
	// dwell is how long the message has been viewed whole: all of it
	// on screen, or, if it is taller than the screen, the screen full
	// of it, with the window having the focus.
	dwell time.Duration
	// since is when the message was last found being viewed whole, or
	// zero if it was not.
	since time.Time
}

// arrivals are the messages that arrived in an open conversation and have
// not been read yet, in the order they arrived.
type arrivals struct {
	list []*arrival
}

// add adds the messages with the IDs ids.
func (a *arrivals) add(ids ...string) {
	for _, id := range ids {
		if !a.has(id) {
			a.list = append(a.list, &arrival{id: id})
		}
	}
}

// has returns whether the message with the ID id has arrived and not been
// read.
func (a *arrivals) has(id string) bool {
	for _, m := range a.list {
		if m.id == id {
			return true
		}
	}
	return false
}

// ids returns the IDs of the messages not read yet.
func (a *arrivals) ids() []string {
	var ret []string
	for _, m := range a.list {
		ret = append(ret, m.id)
	}
	return ret
}

// update records what is viewed at now, and returns the IDs of the
// messages that have been read since the last update, which are no
// longer among a. spans holds, by ID, the [start, end) lines of each
// message of the conversation; a message not in it has gone from the
// conversation, and is dropped without being read. Lines [top, bottom)
// are on screen, and focused is whether the window has the focus.
//
// The time from the last update to now counts as viewing a message if it
// was viewed whole at the last update. A message is read once every line
// of it has been seen and it has been viewed whole for arrivalReadTime in
// all.
func (a *arrivals) update(now time.Time, spans map[string][2]int,
	top, bottom int, focused bool) []string {
	var read []string
	keep := a.list[:0]
	for _, m := range a.list {
		span, ok := spans[m.id]
		if !ok {
			continue
		}
		if !m.since.IsZero() {
			m.dwell += now.Sub(m.since)
		}
		m.since = time.Time{}
		start, end := span[0], span[1]
		if len(m.seen) != end-start {
			// Laid out again, at another width, or with its
			// quotes shown or hidden: what was seen of it is not
			// known by line any more.
			m.seen = make([]bool, end-start)
		}
		if focused {
			for l := max(start, top); l < min(end, bottom); l++ {
				m.seen[l-start] = true
			}
			whole := start >= top && end <= bottom
			fills := start <= top && end >= bottom
			if whole || fills {
				m.since = now
			}
		}
		if m.dwell >= arrivalReadTime && allSeen(m.seen) {
			read = append(read, m.id)
			continue
		}
		keep = append(keep, m)
	}
	a.list = keep
	return read
}

// allSeen returns whether every element of seen is set.
func allSeen(seen []bool) bool {
	for _, s := range seen {
		if !s {
			return false
		}
	}
	return true
}

// newArrivals returns the IDs of the messages of msgs that are not among
// old, but for those sent from this account and drafts, which arrive by
// replying in the conversation rather than by being received. Nothing has
// arrived when old is empty, as then the conversation is being opened.
func newArrivals(old, msgs []*cmdg.Message) []string {
	if len(old) == 0 {
		return nil
	}
	had := map[string]bool{}
	for _, m := range old {
		had[m.ID] = true
	}
	var ret []string
	for _, m := range msgs {
		if had[m.ID] || m.HasLabel(cmdg.Sent) ||
			m.HasLabel(draftLabel) {
			continue
		}
		ret = append(ret, m.ID)
	}
	return ret
}

// polledNew returns whether a check of the thread, which found messages
// with the IDs ids and the labels labels, found one that should be in the
// conversation of rep but is not among msgs, those shown.
func polledNew(ids []string, labels [][]string, msgs []*cmdg.Message,
	rep *cmdg.Message) bool {
	shown := map[string]bool{}
	for _, m := range msgs {
		shown[m.ID] = true
	}
	for n, id := range ids {
		if !shown[id] && !hidden(labels[n], rep) {
			return true
		}
	}
	return false
}

// arrivalNotice is the line telling of the messages not read yet, from
// the senders froms, the first of which is message pos, counting from 1,
// of the conversation's total.
func arrivalNotice(froms []string, pos, total int) string {
	if len(froms) == 1 {
		return fmt.Sprintf("New message from %s (message %d of %d)",
			froms[0], pos, total)
	}
	return fmt.Sprintf("%d new messages, from %s (from message %d of %d)",
		len(froms), strings.Join(froms, ", "), pos, total)
}
