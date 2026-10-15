package cmdg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	gmail "google.golang.org/api/gmail/v1"
)

// fakeThreads serves thread T, of messages m1 and m2, and records the
// thread modifications asked for.
type fakeThreads struct {
	m        sync.Mutex
	modifies []string
}

func (f *fakeThreads) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/threads/T/modify"):
		b, _ := io.ReadAll(r.Body)
		f.m.Lock()
		f.modifies = append(f.modifies, strings.TrimSpace(string(b)))
		f.m.Unlock()
		_ = json.NewEncoder(w).Encode(&gmail.Thread{Id: "T"})
	case strings.HasSuffix(p, "/threads/T"):
		_ = json.NewEncoder(w).Encode(&gmail.Thread{Id: "T",
			Messages: []*gmail.Message{
				{Id: "m1", LabelIds: []string{Inbox}},
				{Id: "m2", LabelIds: []string{Trash}}}})
	case strings.Contains(p, "/messages/"):
		id := p[strings.LastIndex(p, "/")+1:]
		body := base64.URLEncoding.EncodeToString(
			[]byte("body of " + id))
		_ = json.NewEncoder(w).Encode(&gmail.Message{
			Id:       id,
			ThreadId: "T",
			Payload: &gmail.MessagePart{
				MimeType: "text/plain",
				Body:     &gmail.MessagePartBody{Data: body},
			},
		})
	default:
		http.NotFound(w, r)
	}
}

func TestThreadMessages(t *testing.T) {
	f := &fakeThreads{}
	ts := httptest.NewServer(f)
	defer ts.Close()
	conn, err := NewFake(&http.Client{Transport: &redirector{ts.URL}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// A message the message list already holds is the one returned.
	listed := NewMessage(conn, "m2")
	msgs, err := conn.ThreadMessages(ctx, "T")
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, m := range msgs {
		b, err := m.GetBody(ctx)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	if want := []string{"body of m1", "body of m2"}; !reflect.DeepEqual(
		bodies, want) {
		t.Errorf("got bodies %q, want %q", bodies, want)
	}
	if msgs[1] != listed {
		t.Errorf("got a new *Message for m2, want the cached one")
	}

	if err := conn.ModifyThread(ctx, "T", []string{Trash},
		[]string{Inbox}); err != nil {
		t.Fatal(err)
	}
	want := `{"addLabelIds":["TRASH"],"removeLabelIds":["INBOX"]}`
	if !reflect.DeepEqual(f.modifies, []string{want}) {
		t.Errorf("got modifications %q, want %q", f.modifies, want)
	}
}

func TestThreadMessageLabels(t *testing.T) {
	ts := httptest.NewServer(&fakeThreads{})
	defer ts.Close()
	conn, err := NewFake(&http.Client{Transport: &redirector{ts.URL}})
	if err != nil {
		t.Fatal(err)
	}
	ids, labels, err := conn.ThreadMessageLabels(context.Background(),
		"T")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"m1", "m2"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("got IDs %q, want %q", ids, want)
	}
	want := [][]string{{Inbox}, {Trash}}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("got labels %q, want %q", labels, want)
	}
}
