package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/api/gmail/v1"

	"github.com/ThomasHabets/cmdg/pkg/cmdg"
)

// fakeHeaders serves each message it is asked for with the From header,
// subject and snippet it holds for it, without a network.
type fakeHeaders map[string][3]string

func (f fakeHeaders) RoundTrip(r *http.Request) (*http.Response, error) {
	p := r.URL.Path
	if !strings.Contains(p, "/messages/") {
		return nil, errors.New("not served in tests: " + p)
	}
	id := p[strings.LastIndex(p, "/")+1:]
	h := f[id]
	rec := httptest.NewRecorder()
	_ = json.NewEncoder(rec).Encode(&gmail.Message{
		Id: id, ThreadId: id, Snippet: h[2],
		Payload: &gmail.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmail.MessagePartHeader{
				{Name: "From", Value: h[0]},
				{Name: "Subject", Value: h[1]},
			},
		},
	})
	return rec.Result(), nil
}

// TestFilterPage checks which messages of a page a filtered list keeps.
func TestFilterPage(t *testing.T) {
	f := fakeHeaders{
		"a": {"Ann <ann@example.com>", "Invoice #12", "Please pay"},
		"b": {"Bob <bob@example.org>", "Lunch?", "Invoice attached"},
		"c": {"Cat <cat@example.com>", "Hello", "Nothing here"},
	}
	conn, err := cmdg.NewFake(&http.Client{Transport: f})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		filters []string
		want    string
	}{
		{nil, "abc"},
		// The From header, with the address as well as the name.
		{[]string{`example\.com`}, "ac"},
		// The subject or the snippet, case insensitive.
		{[]string{"INVOICE"}, "ab"},
		{[]string{`#\d+`}, "a"},
		// Every filter has to match, each in any field.
		{[]string{"invoice", "example.com"}, "a"},
		{[]string{"zzz"}, ""},
		// Not a regexp, so matched as written.
		{[]string{"lunch?("}, ""},
	} {
		var fs []*regexp.Regexp
		for _, s := range tc.filters {
			fs = append(fs, compileFilter(s))
		}
		mv := &MessageView{filters: fs}
		page := &cmdg.Page{Response: &gmail.ListMessagesResponse{}}
		for _, id := range []string{"a", "b", "c"} {
			page.Messages = append(page.Messages,
				cmdg.NewMessage(conn, id))
			page.Response.Messages = append(page.Response.Messages,
				&gmail.Message{Id: id, ThreadId: id})
		}
		mv.filterPage(context.Background(), page)
		got := ""
		for n, m := range page.Messages {
			got += m.ID
			if page.Response.Messages[n].Id != m.ID {
				t.Errorf("%q: response %d is %q, message %q",
					tc.filters, n,
					page.Response.Messages[n].Id, m.ID)
			}
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q",
				tc.filters, got, tc.want)
		}
		resp := page.Response.Messages
		if len(resp) != len(page.Messages) {
			t.Errorf("%q: %d responses for %d messages",
				tc.filters, len(resp), len(page.Messages))
		}
	}
}

// TestCompileFilter checks that a filter is case insensitive, and that
// one not a regexp is matched as written.
func TestCompileFilter(t *testing.T) {
	for _, tc := range []struct {
		filter, s string
		want      bool
	}{
		{"foo", "FOO bar", true},
		{`f.o`, "fxo", true},
		{"a(", "a(", true},
		{"a(", "a", false},
	} {
		got := compileFilter(tc.filter).MatchString(tc.s)
		if got != tc.want {
			t.Errorf("%q on %q: got %v, want %v",
				tc.filter, tc.s, got, tc.want)
		}
	}
}
