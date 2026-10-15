package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
	"github.com/ThomasHabets/cmdg/pkg/display"
)

// TestArrivalsUpdate checks when a message that has arrived counts as
// read, through sequences of what is on screen, one update a second or
// so, as the conversation view makes them.
func TestArrivalsUpdate(t *testing.T) {
	// The message is lines 20-30 of the conversation; a screen of 15
	// lines shows it whole from scroll 15 to 20.
	short := map[string][2]int{"m": {20, 30}, "old": {0, 18}}
	// The message is lines 20-60, taller than the screen.
	tall := map[string][2]int{"m": {20, 60}}
	// The short message laid out wider, 5 lines shorter.
	wide := map[string][2]int{"m": {20, 25}}

	type step struct {
		at      int // Seconds since the start.
		spans   map[string][2]int
		top     int
		focused bool
	}
	view := func(at, top int) step { return step{at, short, top, true} }
	for _, tc := range []struct {
		name   string
		height int
		steps  []step
		// readAt is the step whose update returns the message as
		// read, or -1 if none does.
		readAt int
	}{
		{"whole on screen for 10 s", 15,
			[]step{view(0, 15), view(5, 15), view(9, 15),
				view(10, 15)},
			3},
		{"not read before 10 s", 15,
			[]step{view(0, 15), view(9, 15)}, -1},
		{"time adds up across looking away", 15,
			[]step{view(0, 15), view(6, 0), view(20, 15),
				view(23, 15), view(24, 15)},
			4},
		{"half on screen is not whole", 15,
			[]step{view(0, 10), view(30, 10)}, -1},
		{"not focused, not viewed", 15,
			[]step{{0, short, 15, false}, {30, short, 15, false}},
			-1},
		{"losing the focus stops the clock", 15,
			[]step{view(0, 15), {5, short, 15, false},
				{30, short, 15, false}, view(31, 15),
				view(35, 15), view(36, 15)},
			5},
		{"tall, scrolled through, then 10 s filling the screen", 15,
			[]step{{0, tall, 20, true}, {4, tall, 30, true},
				{8, tall, 45, true}, {10, tall, 45, true}},
			3},
		{"tall, the middle never shown", 15,
			[]step{{0, tall, 20, true}, {6, tall, 45, true},
				{20, tall, 45, true}},
			-1},
		// Seen, then laid out again with none of it on screen:
		// the time viewed is kept, but it must be seen again.
		{"laid out again, what was seen forgotten", 15,
			[]step{view(0, 15), view(9, 15), {9, wide, 0, true},
				{12, wide, 0, true}, {12, wide, 15, true},
				{13, wide, 15, true}},
			5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var a arrivals
			a.add("m")
			start := time.Now()
			for n, s := range tc.steps {
				now := start.Add(
					time.Duration(s.at) * time.Second)
				read := a.update(now, s.spans, s.top,
					s.top+tc.height, s.focused)
				var want []string
				if n == tc.readAt {
					want = []string{"m"}
				}
				if !reflect.DeepEqual(read, want) {
					t.Fatalf("step %d: read %q, want %q",
						n, read, want)
				}
			}
			if wantLeft := tc.readAt < 0; a.has("m") != wantLeft {
				t.Errorf("still not read: %v, want %v",
					a.has("m"), wantLeft)
			}
		})
	}
}

// TestArrivalsGone checks that a message that leaves the conversation is
// dropped without being read, and that the others are kept in order.
func TestArrivalsGone(t *testing.T) {
	var a arrivals
	a.add("x", "y", "z", "y")
	got, want := a.ids(), []string{"x", "y", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("added %q, want %q, each once", got, want)
	}
	read := a.update(time.Now(), map[string][2]int{"x": {0, 5},
		"z": {10, 15}}, 100, 110, true)
	if read != nil {
		t.Errorf("read %q, want none", read)
	}
	got, want = a.ids(), []string{"x", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("left %q, want %q", got, want)
	}
}

// labelled makes messages with the given IDs and labels, as pairs of ID
// and comma-free label list.
func labelled(t *testing.T, pairs ...any) []*cmdg.Message {
	conn := fakeConn(t)
	var ret []*cmdg.Message
	for n := 0; n < len(pairs); n += 2 {
		id := pairs[n].(string)
		resp := &gmail.Message{Id: id, ThreadId: "T",
			LabelIds: pairs[n+1].([]string)}
		ret = append(ret, cmdg.NewMessageWithResponse(conn, id, resp,
			cmdg.LevelMinimal))
	}
	return ret
}

func TestNewArrivals(t *testing.T) {
	inbox := []string{cmdg.Inbox, cmdg.Unread}
	msgs := labelled(t, "a", inbox, "b", inbox, "mine",
		[]string{cmdg.Sent}, "draft", []string{draftLabel},
		"c", inbox)
	for _, tc := range []struct {
		name string
		old  []*cmdg.Message
		want []string
	}{
		{"opening, nothing arrived", nil, nil},
		{"new ones, not those sent or drafts", msgs[:1],
			[]string{"b", "c"}},
		{"none new", msgs, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := newArrivals(tc.old, msgs)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPolledNew(t *testing.T) {
	inbox := []string{cmdg.Inbox}
	shown := labelled(t, "a", inbox, "b", inbox)
	rep := shown[0]
	for _, tc := range []struct {
		name   string
		ids    []string
		labels [][]string
		want   bool
	}{
		{"same", []string{"a", "b"}, [][]string{inbox, inbox}, false},
		{"one gone", []string{"a"}, [][]string{inbox}, false},
		{"one new", []string{"a", "b", "c"},
			[][]string{inbox, inbox, inbox}, true},
		{"new but in the trash, not shown", []string{"a", "b", "c"},
			[][]string{inbox, inbox, {cmdg.Trash}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := polledNew(tc.ids, tc.labels, shown, rep)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestArrivalNotice(t *testing.T) {
	if got, want := arrivalNotice([]string{"Bob"}, 5, 5),
		"New message from Bob (message 5 of 5)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := arrivalNotice([]string{"Bob", "Ann"}, 4, 5),
		"2 new messages, from Bob, Ann (from message 4 of 5)"; got !=
		want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestSpans checks that a message's span leaves out the blank line and
// rule put before the next one.
func TestSpans(t *testing.T) {
	tv := &ThreadView{
		msgs:   labelled(t, "a", []string{}, "b", []string{}),
		starts: []int{0, 7},
		lines:  make([]string, 12),
	}
	got := tv.spans()
	want := map[string][2]int{"a": {0, 5}, "b": {7, 12}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// fakeGmail serves each message it is asked for, from the sender named
// by its ID, with a body of ten lines, and the labels labels holds for it,
// without a network.
type fakeGmail struct {
	labels map[string][]string
}

func (f fakeGmail) RoundTrip(r *http.Request) (*http.Response, error) {
	p := r.URL.Path
	if !strings.Contains(p, "/messages/") {
		return nil, errors.New("not served in tests: " + p)
	}
	id := p[strings.LastIndex(p, "/")+1:]
	body := base64.URLEncoding.EncodeToString(
		[]byte(strings.Repeat("line\n", 10)))
	rec := httptest.NewRecorder()
	_ = json.NewEncoder(rec).Encode(&gmail.Message{
		Id: id, ThreadId: "T", LabelIds: f.labels[id],
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: id},
			},
			Body: &gmail.MessagePartBody{Data: body},
		},
	})
	return rec.Result(), nil
}

// loadedMessages makes messages of thread T, served by fakeGmail, given as
// pairs of ID and labels.
func loadedMessages(t *testing.T, pairs ...any) []*cmdg.Message {
	f := fakeGmail{labels: map[string][]string{}}
	conn, err := cmdg.NewFake(&http.Client{Transport: f})
	if err != nil {
		t.Fatal(err)
	}
	var ret []*cmdg.Message
	for n := 0; n < len(pairs); n += 2 {
		id := pairs[n].(string)
		f.labels[id] = pairs[n+1].([]string)
		m := cmdg.NewMessage(conn, id)
		if err := m.Preload(context.Background(),
			cmdg.LevelFull); err != nil {
			t.Fatal(err)
		}
		ret = append(ret, m)
	}
	return ret
}

// TestReceive checks how the conversation view takes a conversation
// loaded again: which messages have arrived, and that the selected
// message stays selected, where it is on screen.
func TestReceive(t *testing.T) {
	ctx := context.Background()
	inbox := []string{cmdg.Inbox}
	m := loadedMessages(t, "a", inbox, "b", inbox, "c", inbox,
		"mine", []string{cmdg.Sent})
	a, b, c, mine := m[0], m[1], m[2], m[3]
	tv := &ThreadView{
		ov: &OpenMessageView{
			screen: &display.Screen{Width: 40, Height: 16},
			errors: make(chan error, 20),
		},
		rep:        a,
		showQuotes: map[string]bool{},
	}

	scroll, arrived := tv.receive(ctx, []*cmdg.Message{a, b}, 0)
	if arrived != nil {
		t.Errorf("opened: arrived %q, want none", arrived)
	}
	if tv.sel != 1 || scroll != tv.starts[1] {
		t.Fatalf("opened: selected %d at scroll %d, want the last, "+
			"read, message at its start %d", tv.sel, scroll,
			tv.starts[1])
	}

	// Scrolled 3 lines into b.
	scroll += 3
	scroll, arrived = tv.receive(ctx, []*cmdg.Message{a, b, c, mine},
		scroll)
	if want := []string{"c"}; !reflect.DeepEqual(arrived, want) {
		t.Errorf("arrived %q, want %q, not the one sent", arrived,
			want)
	}
	if tv.msgs[tv.sel] != b || scroll != tv.starts[tv.sel]+3 {
		t.Errorf("after arriving: selected %q at scroll %d, want "+
			"b, 3 lines into it at %d", tv.msgs[tv.sel].ID,
			scroll, tv.starts[tv.sel]+3)
	}
	if !tv.arrived.has("c") || tv.arrived.has("mine") {
		t.Errorf("waiting to be read: %q, want c", tv.arrived.ids())
	}

	// a is trashed, so that b moves up the conversation.
	scroll, arrived = tv.receive(ctx, []*cmdg.Message{b, c, mine},
		scroll)
	if arrived != nil {
		t.Errorf("after a went: arrived %q, want none", arrived)
	}
	if tv.sel != 0 || scroll != tv.starts[0]+3 {
		t.Errorf("after a went: selected %d at scroll %d, want b, "+
			"0, 3 lines into it at %d", tv.sel, scroll,
			tv.starts[0]+3)
	}
}
