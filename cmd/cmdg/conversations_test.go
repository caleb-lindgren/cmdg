package main

import (
	"reflect"
	"testing"

	"google.golang.org/api/gmail/v1"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
)

// convFixture makes messages with the IDs and threads given as pairs, all
// unread and in the inbox, in a connection of their own, and a
// conversations knowing their threads.
func convFixture(t *testing.T, on bool,
	pairs ...string) (*conversations, map[string]*cmdg.Message) {
	conn := fakeConn(t)
	c := newConversations(on)
	msgs := map[string]*cmdg.Message{}
	for n := 0; n < len(pairs); n += 2 {
		resp := &gmail.Message{Id: pairs[n], ThreadId: pairs[n+1],
			LabelIds: []string{cmdg.Inbox, cmdg.Unread}}
		msgs[pairs[n]] = cmdg.NewMessageWithResponse(conn, pairs[n],
			resp, cmdg.LevelMinimal)
		c.note(resp)
	}
	return c, msgs
}

func ids(msgs []*cmdg.Message) []string {
	var ret []string
	for _, m := range msgs {
		ret = append(ret, m.ID)
	}
	return ret
}

func memberIDs(c *conversations) map[string][]string {
	ret := map[string][]string{}
	for rep, ms := range c.members {
		if len(ms) > 0 {
			ret[rep] = ids(ms)
		}
	}
	return ret
}

// TestRegroup checks the grouping of message list rows by thread through
// a sequence of changes, each made as upstream's message list code makes
// it, to the rows the previous grouping returned.
func TestRegroup(t *testing.T) {
	c, m := convFixture(t, true,
		"a3", "A", "b2", "B", "a2", "A", "c1", "C", "a1", "A",
		"a4", "A", "x", "")
	rows := []*cmdg.Message{m["a3"], m["b2"], m["a2"], m["c1"],
		m["a1"]}

	steps := []struct {
		name        string
		change      func([]*cmdg.Message) []*cmdg.Message
		wantRows    []string
		wantMembers map[string][]string
	}{
		{"first page",
			func(r []*cmdg.Message) []*cmdg.Message { return r },
			[]string{"a3", "b2", "c1"},
			map[string][]string{"a3": {"a2", "a1"}}},
		{"new message in a listed thread, prepended",
			func(r []*cmdg.Message) []*cmdg.Message {
				return append([]*cmdg.Message{m["a4"]}, r...)
			},
			[]string{"a4", "b2", "c1"},
			map[string][]string{"a4": {"a3", "a2", "a1"}}},
		{"message of unknown thread, a row of its own",
			func(r []*cmdg.Message) []*cmdg.Message {
				return append([]*cmdg.Message{m["x"]}, r...)
			},
			[]string{"x", "a4", "b2", "c1"},
			map[string][]string{"a4": {"a3", "a2", "a1"}}},
		{"row dropped, as archiving does, takes its members",
			func(r []*cmdg.Message) []*cmdg.Message {
				return append(r[:1:1], r[2:]...)
			},
			[]string{"x", "b2", "c1"},
			map[string][]string{}},
	}
	for _, s := range steps {
		var repOf map[string]string
		rows, repOf = c.regroup(s.change(rows))
		if got := ids(rows); !reflect.DeepEqual(got, s.wantRows) {
			t.Fatalf("%s: got rows %q, want %q", s.name, got,
				s.wantRows)
		}
		if got := memberIDs(c); !reflect.DeepEqual(got,
			s.wantMembers) {
			t.Fatalf("%s: got members %q, want %q", s.name, got,
				s.wantMembers)
		}
		for rep, ms := range s.wantMembers {
			for _, id := range ms {
				if repOf[id] != rep {
					t.Errorf("%s: %s in row %q, want %q",
						s.name, id, repOf[id], rep)
				}
			}
		}
	}
}

// TestRegroupOff checks that turning grouping off puts each row's members
// back in the list after it.
func TestRegroupOff(t *testing.T) {
	c, m := convFixture(t, true,
		"a2", "A", "b1", "B", "a1", "A")
	rows, _ := c.regroup([]*cmdg.Message{m["a2"], m["b1"], m["a1"]})
	c.on = false
	rows, _ = c.regroup(rows)
	if got, want := ids(rows), []string{"a2", "a1", "b1"}; !reflect.
		DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if len(c.members) != 0 {
		t.Errorf("got members %q, want none", memberIDs(c))
	}
}

func TestExpand(t *testing.T) {
	c, m := convFixture(t, true,
		"a2", "A", "b1", "B", "a1", "A")
	c.regroup([]*cmdg.Message{m["a2"], m["b1"], m["a1"]})
	got := c.expand([]string{"b1", "a2"})
	if want := []string{"b1", "a2", "a1"}; !reflect.DeepEqual(got,
		want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestMemberChanges checks that changes to messages that are members of a
// row, which the message list looks up by row and so does not find, are
// applied: from the history, and from a message window.
func TestMemberChanges(t *testing.T) {
	c, m := convFixture(t, true,
		"a3", "A", "a2", "A", "a1", "A")
	c.regroup([]*cmdg.Message{m["a3"], m["a2"], m["a1"]})
	if !c.unread(m["a3"]) {
		t.Fatalf("row not unread")
	}

	ref := func(id string) *gmail.Message {
		return &gmail.Message{Id: id, ThreadId: "A"}
	}
	c.noteHistory([]*gmail.History{{
		LabelsRemoved: []*gmail.HistoryLabelRemoved{
			{Message: ref("a2"), LabelIds: []string{cmdg.Unread}},
			{Message: ref("a1"), LabelIds: []string{cmdg.Inbox}},
		},
		LabelsAdded: []*gmail.HistoryLabelAdded{
			{Message: ref("a2"), LabelIds: []string{"Label_1"}},
		},
	}}, cmdg.Inbox)
	if m["a2"].IsUnread() || !m["a2"].HasLabel("Label_1") {
		t.Errorf("a2 has labels %q, want Label_1 and not UNREAD",
			m["a2"].LocalLabels())
	}
	if got, want := memberIDs(c), map[string][]string{
		"a3": {"a2"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("after history, got members %q, want %q", got, want)
	}

	m["a3"].RemoveLabelIDLocal(cmdg.Unread)
	if c.unread(m["a3"]) {
		t.Errorf("row unread with all its messages read")
	}
	m["a2"].AddLabelIDLocal(cmdg.Unread)
	if !c.unread(m["a3"]) {
		t.Errorf("row read with member a2 unread")
	}

	c.applyWindowEvent(windowEvent{ID: "a2",
		RemoveLabels: []string{cmdg.Inbox}, Drop: true}, cmdg.Inbox)
	if got := memberIDs(c); len(got) != 0 {
		t.Errorf("after window event, got members %q, want none",
			got)
	}
}

func TestFromColumn(t *testing.T) {
	c, m := convFixture(t, true, "a2", "A", "a1", "A", "b1", "B")
	c.regroup([]*cmdg.Message{m["a2"], m["a1"], m["b1"]})
	for _, tc := range []struct {
		id, from, want string
	}{
		{"a2", "Alice Example", "    Alice Example (2)"},
		{"a2", "A very long sender name",
			"A very long sende (2)"},
		{"b1", "Bob", "                  Bob"},
	} {
		if got := c.fromColumn(m[tc.id], tc.from, 21); got != tc.want {
			t.Errorf("fromColumn(%s, %q) = %q, want %q", tc.id,
				tc.from, got, tc.want)
		}
	}
}

func TestAddHelp(t *testing.T) {
	got := addHelp("a — A\nq — Quit\n\nPress [enter] to exit\n",
		"T — Toggle\n")
	want := "a — A\nq — Quit\nT — Toggle\n\nPress [enter] to exit\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCountFromThread checks that a row counts what its conversation
// shows once the thread is fetched: the messages of the thread not in the
// trash or spam, listed or not, rather than the listed messages.
func TestCountFromThread(t *testing.T) {
	c, m := convFixture(t, true, "a2", "A", "a1", "A")
	c.regroup([]*cmdg.Message{m["a2"], m["a1"]})
	if got := c.count(m["a2"]); got != 2 {
		t.Errorf("before fetching, got %d, want the 2 listed", got)
	}
	c.threads["A"] = [][]string{{cmdg.Inbox}, {"SENT"},
		{cmdg.Trash}, {cmdg.Inbox}}
	if got := c.count(m["a2"]); got != 3 {
		t.Errorf("fetched, got %d, want 3 not in the trash", got)
	}
	c.noteHistory([]*gmail.History{{Messages: []*gmail.Message{
		{Id: "a3", ThreadId: "A"}}}}, cmdg.Inbox)
	if got := c.count(m["a2"]); got != 2 {
		t.Errorf("after a change to the thread, got %d, want the "+
			"2 listed until it is fetched again", got)
	}
}

// TestPrependRow checks that a message the history check puts on top of the
// list leaves the cursor on the row it was on, once regrouped, and where
// the rows go on screen.
func TestPrependRow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		on         bool
		pos        int
		scroll     int
		height     int
		wantRow    string
		wantScroll int
	}{
		{"below the thread joined", true, 3, 0, 10, "x1", 0},
		{"on the thread joined, which moves up", true, 0, 0, 10,
			"a2", 0},
		{"below the thread joined, next to it", true, 1, 0, 10,
			"b1", 0},
		{"not grouped", false, 3, 0, 10, "x1", 0},
		{"at the bottom of the screen", false, 2, 0, 3, "c1", 1},
		{"scrolled, rows stay on screen", false, 3, 2, 10, "x1", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, m := convFixture(t, tc.on, "a1", "A", "b1", "B",
				"c1", "C", "x1", "X", "a2", "A")
			mv := &MessageView{conv: c, pos: tc.pos,
				messages: []*cmdg.Message{m["a1"], m["b1"],
					m["c1"], m["x1"]}}
			scroll := tc.scroll
			mv.prependRow(m["a2"], &scroll, tc.height)
			mv.regroup(map[string]bool{}, &scroll)
			if got := mv.messages[mv.pos].ID; got != tc.wantRow {
				t.Errorf("cursor on %q, want %q", got,
					tc.wantRow)
			}
			if scroll != tc.wantScroll {
				t.Errorf("scroll %d, want %d", scroll,
					tc.wantScroll)
			}
		})
	}
}
