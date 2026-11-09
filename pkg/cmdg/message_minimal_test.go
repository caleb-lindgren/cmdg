package cmdg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gmail "google.golang.org/api/gmail/v1"
)

// TestThreadIDFromMinimal checks that loading a message at the minimal
// level, which Gmail answers without a payload, does not dereference the
// missing payload. A conversation window asks for the thread ID of the
// message it was opened on, and crashed here when that was its first fetch.
func TestThreadIDFromMinimal(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("format"); got != "minimal" {
				t.Errorf("format = %q, want minimal", got)
			}
			_ = json.NewEncoder(w).Encode(&gmail.Message{
				Id:       "m1",
				ThreadId: "T",
				LabelIds: []string{Inbox},
			})
		}))
	defer ts.Close()
	conn, err := NewFake(&http.Client{Transport: &redirector{ts.URL}})
	if err != nil {
		t.Fatal(err)
	}
	tid, err := NewMessage(conn, "m1").ThreadID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tid != "T" {
		t.Errorf("thread ID = %q, want T", tid)
	}
}
