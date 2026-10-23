package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/api/gmail/v1"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
	"github.com/ThomasHabets/cmdg/pkg/display"
)

// threadServer answers the Gmail API requests for a thread, T, of the
// messages bodies, keyed by ID, in the order of ids.
type threadServer struct {
	ids    []string
	bodies map[string]string
}

func (s *threadServer) RoundTrip(r *http.Request) (*http.Response, error) {
	p := r.URL.Path
	var v interface{}
	if strings.HasSuffix(p, "/threads/T") {
		th := &gmail.Thread{Id: "T"}
		for _, id := range s.ids {
			th.Messages = append(th.Messages,
				&gmail.Message{Id: id})
		}
		v = th
	} else {
		id := p[strings.LastIndex(p, "/")+1:]
		body := base64.URLEncoding.EncodeToString(
			[]byte(s.bodies[id]))
		v = &gmail.Message{
			Id:       id,
			ThreadId: "T",
			LabelIds: []string{cmdg.Inbox},
			Payload: &gmail.MessagePart{
				MimeType: "text/plain",
				Headers: []*gmail.MessagePartHeader{
					{Name: "From", Value: "from-" + id},
				},
				Body: &gmail.MessagePartBody{Data: body},
			},
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
		Request:    r,
	}, nil
}

// TestThreadBuild lays out a conversation of a message and a reply quoting
// it, with quotes hidden and shown.
func TestThreadBuild(t *testing.T) {
	old := conn
	t.Cleanup(func() { conn = old })
	var err error
	conn, err = cmdg.NewFake(&http.Client{Transport: &threadServer{
		ids: []string{"m1", "m2"},
		bodies: map[string]string{
			"m1": "Lunch?\n> earlier",
			"m2": "Yes.\n\nOn Mon, Alice wrote:\n> Lunch?\n",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	msgs, err := conn.ThreadMessages(ctx, "T")
	if err != nil {
		t.Fatal(err)
	}
	tv := &ThreadView{rep: msgs[1], msgs: msgs,
		showQuotes: map[string]bool{}}

	plain := func() []string {
		var ret []string
		for _, l := range tv.lines {
			for _, c := range []string{display.Bold, display.Grey,
				display.Reset} {
				l = strings.ReplaceAll(l, c, "")
			}
			ret = append(ret, l)
		}
		return ret
	}

	tv.build(ctx, 40)
	got := strings.Join(plain(), "\n")
	want := strings.Join([]string{
		"from-m1",
		"Date: " + mustDate(t, ctx, msgs[0]),
		"",
		"Lunch?",
		"[… 1 quoted line, z to show]",
		"",
		strings.Repeat("─", 40),
		"from-m2",
		"Date: " + mustDate(t, ctx, msgs[1]),
		"",
		"Yes.",
		"",
		"[… 2 quoted lines, z to show]",
	}, "\n")
	if got != want {
		t.Errorf("quotes hidden: got\n%s\nwant\n%s", got, want)
	}
	if want := []int{0, 7}; len(tv.starts) != 2 ||
		tv.starts[0] != want[0] || tv.starts[1] != want[1] {
		t.Errorf("got starts %v, want %v", tv.starts, want)
	}
	if got := tv.current(8); got != 1 {
		t.Errorf("message at line 8 is %d, want 1", got)
	}

	// Showing the quotes of m2 leaves those of m1 hidden.
	tv.showQuotes["m2"] = true
	tv.build(ctx, 40)
	got = strings.Join(plain()[10:], "\n")
	if want := "Yes.\n\nOn Mon, Alice wrote:\n> Lunch?"; got != want {
		t.Errorf("quotes of m2 shown: got\n%s\nwant\n%s", got, want)
	}
	got, want = plain()[4], "[… 1 quoted line, z to show]"
	if got != want {
		t.Errorf("quotes of m1: got %q, want %q", got, want)
	}
}

// TestThreadFollow checks that scrolling the selected message off screen
// selects the message at the edge it left by, and that otherwise the
// selection stays, which lets it move to messages near the end that can
// not be scrolled to the top.
func TestThreadFollow(t *testing.T) {
	// 4 lines of messages: 10 less the 4 lines above and 2 below.
	tv := &ThreadView{
		ov:     &OpenMessageView{screen: &display.Screen{Height: 10}},
		lines:  make([]string, 30),
		starts: []int{0, 10, 27},
	}
	for _, tc := range []struct {
		name        string
		sel, scroll int
		want        int
	}{
		{"still on screen", 0, 8, 0},
		{"scrolled off the top", 0, 12, 1},
		{"scrolled off the bottom", 2, 5, 0},
		{"off the bottom, next one at the edge", 2, 7, 1},
		{"last of the ones at the bottom", 2, 26, 2},
		{"first of the ones at the bottom", 1, 26, 1},
	} {
		tv.sel = tc.sel
		tv.follow(tc.scroll)
		if tv.sel != tc.want {
			t.Errorf("%s: selected %d, want %d", tc.name, tv.sel,
				tc.want)
		}
	}
	if got := tv.clamp(27); got != 26 {
		t.Errorf("clamp(27) = %d, want 26", got)
	}
}

func mustDate(t *testing.T, ctx context.Context, m *cmdg.Message) string {
	t.Helper()
	d, _ := m.GetDateHeader(ctx)
	return d
}
