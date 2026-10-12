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
			"m1": "Lunch?",
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
	tv := &ThreadView{rep: msgs[1], msgs: msgs}

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
	if want := []int{0, 6}; len(tv.starts) != 2 ||
		tv.starts[0] != want[0] || tv.starts[1] != want[1] {
		t.Errorf("got starts %v, want %v", tv.starts, want)
	}
	if got := tv.current(7); got != 1 {
		t.Errorf("message at line 7 is %d, want 1", got)
	}

	tv.showQuotes = true
	tv.build(ctx, 40)
	got = strings.Join(plain()[9:], "\n")
	if want := "Yes.\n\nOn Mon, Alice wrote:\n> Lunch?"; got != want {
		t.Errorf("quotes shown: got\n%s\nwant\n%s", got, want)
	}
}

func mustDate(t *testing.T, ctx context.Context, m *cmdg.Message) string {
	t.Helper()
	d, _ := m.GetDateHeader(ctx)
	return d
}
