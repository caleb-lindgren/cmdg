package main

// Conversation mode, added by this fork: the message list shows one row
// per thread rather than one per message, and opening a row shows the
// whole thread.
//
// The list is still built from messages.list, not threads.list, and is
// grouped here, so that upstream's paging, history and batch code keeps
// working on messages. mv.messages holds one message per row, the newest
// listed one of its thread, called the row's representative; the older
// listed messages of the thread are kept aside as the row's members.
// Upstream code that finds a message through messagePos therefore finds
// representatives only, which is what it needs to move, draw and drop
// rows, and the few places that act on every message of a row expand the
// representatives into the row's messages with expand.

import (
	"flag"
	"fmt"
	"strings"

	"google.golang.org/api/gmail/v1"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
	"github.com/ThomasHabets/cmdg/pkg/display"
)

var conversationsFlag = flag.Bool("conversations", false,
	"Start the message list in conversation mode, one row per thread. "+
		"T toggles it.")

// conversations is the conversation state of one message list.
type conversations struct {
	// on is whether the list is grouped by thread.
	on bool

	// threadOf maps message ID to thread ID, for every message the
	// list has been told of, grouped or not.
	threadOf map[string]string

	// members maps the ID of each row's representative to the older
	// listed messages of its thread, newest first.
	members map[string][]*cmdg.Message
}

func newConversations(on bool) *conversations {
	return &conversations{
		on:       on,
		threadOf: map[string]string{},
		members:  map[string][]*cmdg.Message{},
	}
}

// note records the thread of each of msgs, as messages.list and the
// history API give them.
func (c *conversations) note(msgs ...*gmail.Message) {
	for _, m := range msgs {
		if m != nil && m.ThreadId != "" {
			c.threadOf[m.Id] = m.ThreadId
		}
	}
}

// noteHistory records the threads of the messages in hists, and applies
// their changes to the messages that are members of a row, which the
// history code of the message list cannot find, as it looks messages up
// by row: label changes, and dropping those deleted or taken out of
// label, the label listed.
func (c *conversations) noteHistory(hists []*gmail.History, label string) {
	for _, h := range hists {
		for _, m := range h.MessagesAdded {
			c.note(m.Message)
		}
		for _, m := range h.MessagesDeleted {
			c.dropMember(m.Message.Id)
		}
		for _, m := range h.LabelsAdded {
			c.note(m.Message)
			for _, msg := range c.member(m.Message.Id) {
				for _, l := range m.LabelIds {
					msg.AddLabelIDLocal(l)
				}
			}
		}
		for _, m := range h.LabelsRemoved {
			c.note(m.Message)
			for _, msg := range c.member(m.Message.Id) {
				for _, l := range m.LabelIds {
					msg.RemoveLabelIDLocal(l)
					if l == label {
						c.dropMember(msg.ID)
					}
				}
			}
		}
	}
}

// dropMember takes the message with the ID id out of the row it is a
// member of, if it is one.
func (c *conversations) dropMember(id string) {
	for rep, ms := range c.members {
		for n, m := range ms {
			if m.ID == id {
				c.members[rep] = append(ms[:n:n], ms[n+1:]...)
				return
			}
		}
	}
}

// member returns the member message with the ID id, as a slice of zero or
// one.
func (c *conversations) member(id string) []*cmdg.Message {
	for _, ms := range c.members {
		for _, m := range ms {
			if m.ID == id {
				return []*cmdg.Message{m}
			}
		}
	}
	return nil
}

// applyWindowEvent applies to a member message the change a message
// window made to it. Changes to representatives are applied by the
// message list itself.
func (c *conversations) applyWindowEvent(ev windowEvent, label string) {
	for _, m := range c.member(ev.ID) {
		drop := ev.Drop
		for _, l := range ev.AddLabels {
			m.AddLabelIDLocal(l)
		}
		for _, l := range ev.RemoveLabels {
			m.RemoveLabelIDLocal(l)
			drop = drop || l == label
		}
		if drop {
			c.dropMember(m.ID)
		}
	}
}

// thread returns the thread ID of msg, or, if it is not known, a key that
// no other message has.
func (c *conversations) thread(msg *cmdg.Message) string {
	if t, ok := c.threadOf[msg.ID]; ok {
		return t
	}
	return "message:" + msg.ID
}

// regroup groups rows, the message list's rows as upstream code left them,
// by thread, and returns one representative per thread in the order of
// the first row of each thread, and a map from the ID of every message in
// rows, or a member of one of them, to the representative of its row.
//
// A row whose thread is already shown further up joins that row, as a row
// added for a new message of a listed thread does, and a row taken out of
// rows takes its members with it, as an archived thread does. When grouping
// is off, it returns rows as they are, with the members of each row
// returned to the list after it, which is where in a list of messages,
// newest first, the older messages of the thread are nearest to.
func (c *conversations) regroup(rows []*cmdg.Message) ([]*cmdg.Message,
	map[string]string) {
	repOf := map[string]string{}
	if !c.on {
		var ret []*cmdg.Message
		for _, r := range rows {
			for _, m := range append([]*cmdg.Message{r},
				c.members[r.ID]...) {
				if _, seen := repOf[m.ID]; !seen {
					repOf[m.ID] = m.ID
					ret = append(ret, m)
				}
			}
		}
		c.members = map[string][]*cmdg.Message{}
		return ret, repOf
	}

	var reps []*cmdg.Message
	byThread := map[string]*cmdg.Message{}
	members := map[string][]*cmdg.Message{}
	add := func(m *cmdg.Message) {
		if _, seen := repOf[m.ID]; seen {
			return
		}
		t := c.thread(m)
		rep, found := byThread[t]
		if !found {
			byThread[t] = m
			reps = append(reps, m)
			repOf[m.ID] = m.ID
			return
		}
		members[rep.ID] = append(members[rep.ID], m)
		repOf[m.ID] = rep.ID
	}
	for _, r := range rows {
		add(r)
		for _, m := range c.members[r.ID] {
			add(m)
		}
	}
	c.members = members
	return reps, repOf
}

// expand returns ids, the IDs of row representatives, with the IDs of the
// members of each row added.
func (c *conversations) expand(ids []string) []string {
	var ret []string
	for _, id := range ids {
		ret = append(ret, id)
		for _, m := range c.members[id] {
			ret = append(ret, m.ID)
		}
	}
	return ret
}

// count returns how many listed messages the row of rep has.
func (c *conversations) count(rep *cmdg.Message) int {
	return 1 + len(c.members[rep.ID])
}

// unread returns whether any listed message in the row of rep is unread.
func (c *conversations) unread(rep *cmdg.Message) bool {
	if rep.IsUnread() {
		return true
	}
	for _, m := range c.members[rep.ID] {
		if m.IsUnread() {
			return true
		}
	}
	return false
}

// fromColumn returns from fitted into width columns, with the number of
// messages in the row of rep after it if there is more than one.
func (c *conversations) fromColumn(rep *cmdg.Message, from string,
	width int) string {
	n := c.count(rep)
	if !c.on || n == 1 {
		return display.FixedWidth(from, width)
	}
	suffix := fmt.Sprintf(" (%d)", n)
	return display.FixedWidth(from, width-len(suffix)) + suffix
}

// listed returns the message with the ID id, a row of the message list
// or a member of one. Messages are cached by ID, so it is the very object
// the list holds, which an index into mv.messages through messagePos is
// not for a member, as messagePos holds rows only.
func listed(id string) *cmdg.Message {
	return cmdg.NewMessage(conn, id)
}

// regroup groups the rows of the message list by thread, or ungroups
// them, as mv.conv.on says, keeping the current row and the marked rows
// where they were. scroll is the list's scroll position.
func (mv *MessageView) regroup(marked map[string]bool, scroll *int) {
	cur := ""
	if mv.pos < len(mv.messages) {
		cur = mv.messages[mv.pos].ID
	}
	rows, repOf := mv.conv.regroup(mv.messages)
	mv.messages = rows
	for id, m := range marked {
		if r, ok := repOf[id]; m && ok && r != id {
			marked[r] = true
			delete(marked, id)
		}
	}
	for n, m := range rows {
		if m.ID == repOf[cur] {
			mv.pos = n
		}
	}
	mv.pos = max(0, min(mv.pos, len(rows)-1))
	*scroll = max(0, min(*scroll, mv.pos))
}

// readFlag returns the window flag that opens a row.
func (c *conversations) readFlag() string {
	if c.on {
		return "-read_conversation"
	}
	return "-read"
}

// conversationListHelp is the help for the message list keys this file
// adds.
const conversationListHelp = `T                  — Toggle one row per thread
`

// addHelp adds the help lines more to help, a view's help text, before the
// line telling how to leave it.
func addHelp(help, more string) string {
	const leave = "\nPress "
	n := strings.LastIndex(help, leave)
	if n < 0 {
		return help + more
	}
	return help[:n] + more + help[n:]
}
