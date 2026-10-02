package cmdg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	gmail "google.golang.org/api/gmail/v1"
)

// fakeMailbox serves the Gmail and People calls LoadCorrespondents makes.
type fakeMailbox struct {
	m        sync.Mutex
	messages []*gmail.Message // Newest first.
	gets     map[string]int   // Message ID -> times fetched.
	noOther  bool             // Refuse Other contacts, as without scope.
}

func (f *fakeMailbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.m.Lock()
	defer f.m.Unlock()
	p := r.URL.Path
	enc := json.NewEncoder(w)
	switch {
	case strings.HasSuffix(p, "/otherContacts"):
		if f.noOther {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"otherContacts": [{
			"names": [{"displayName": "Olga"}],
			"emailAddresses": [{"value": "olga@example.com"}]
		}]}`))
	case strings.HasSuffix(p, "/users/me/profile"):
		_ = enc.Encode(&gmail.Profile{EmailAddress: "Me@example.com"})
	case strings.HasSuffix(p, "/users/me/messages"):
		var r gmail.ListMessagesResponse
		for _, m := range f.messages {
			r.Messages = append(r.Messages,
				&gmail.Message{Id: m.Id})
		}
		_ = enc.Encode(&r)
	case strings.Contains(p, "/users/me/messages/"):
		id := p[strings.LastIndex(p, "/")+1:]
		for _, m := range f.messages {
			if m.Id == id {
				f.gets[id]++
				_ = enc.Encode(m)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type redirector struct{ base string }

func (r *redirector) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.base)
	r2 := req.Clone(req.Context())
	r2.URL.Scheme, r2.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r2)
}

// fakeMessage makes a message with a date, labels, and header name, value
// pairs.
func fakeMessage(id string, date int64, labels []string,
	headers ...string) (m *gmail.Message) {
	m = &gmail.Message{
		Id:           id,
		InternalDate: date,
		LabelIds:     labels,
		Payload:      &gmail.MessagePart{},
	}
	for i := 0; i < len(headers); i += 2 {
		m.Payload.Headers = append(m.Payload.Headers,
			&gmail.MessagePartHeader{
				Name:  headers[i],
				Value: headers[i+1],
			})
	}
	return m
}

func TestLoadCorrespondents(t *testing.T) {
	ctx := context.Background()
	f := &fakeMailbox{
		gets: map[string]int{},
		messages: []*gmail.Message{
			fakeMessage("m2", 2000, []string{"INBOX"},
				"From", "Bob Builder <bob@example.com>",
				"To", "me@example.com, list@example.com"),
			fakeMessage("m1", 1000, []string{"SENT"},
				"From", "me@example.com",
				"To", "carol@example.com, BOB@example.com",
				"Cc", "olga@example.com"),
		},
	}
	ts := httptest.NewServer(f)
	defer ts.Close()
	conn, err := NewFake(&http.Client{Transport: &redirector{ts.URL}})
	if err != nil {
		t.Fatal(err)
	}
	conn.contacts = []string{
		"Alice <alice@example.com>",
		"Carol <carol@example.com>",
	}

	if err := conn.LoadCorrespondents(ctx); err != nil {
		t.Fatal(err)
	}
	// Carol only once, as the contact; Bob with his name from m2; Olga
	// from Other contacts; not me, and not list@, which only received.
	// Bob first, last seen in m2; Carol and Olga, both last seen in m1,
	// alphabetically; Alice, never seen, last.
	want := []string{
		"me",
		`"Bob Builder" <bob@example.com>`,
		"Carol <carol@example.com>",
		"Olga <olga@example.com>",
		"Alice <alice@example.com>",
	}
	if got := conn.Contacts(); !reflect.DeepEqual(got, want) {
		t.Errorf("Contacts() = %q, want %q", got, want)
	}

	// A reload fetches only the new message.
	f.m.Lock()
	f.messages = append([]*gmail.Message{
		fakeMessage("m3", 3000, nil, "From", "dave@example.com"),
	}, f.messages...)
	f.noOther = true
	f.m.Unlock()
	if err := conn.LoadCorrespondents(ctx); err != nil {
		t.Fatal(err)
	}
	wantGets := map[string]int{"m1": 1, "m2": 1, "m3": 1}
	if !reflect.DeepEqual(f.gets, wantGets) {
		t.Errorf("gets = %v, want %v", f.gets, wantGets)
	}
	// A failed Other contacts load keeps the previous result. Dave, from
	// the newest message, goes first.
	want = []string{
		"me",
		"dave@example.com",
		`"Bob Builder" <bob@example.com>`,
		"Carol <carol@example.com>",
		"Olga <olga@example.com>",
		"Alice <alice@example.com>",
	}
	if got := conn.Contacts(); !reflect.DeepEqual(got, want) {
		t.Errorf("Contacts() = %q, want %q", got, want)
	}
}

func TestMessageCorrespondentsDecodesNames(t *testing.T) {
	m := fakeMessage("x", 0, nil,
		"From", "=?iso-8859-2?q?Pawe=B3?= <pawel@example.com>")
	got := messageCorrespondents(m)
	if len(got) != 1 || got[0].Name != "Paweł" {
		t.Errorf("got %v, want one address named Paweł", got)
	}
}

func TestAddressKey(t *testing.T) {
	for in, want := range map[string]string{
		"Bob@Example.com":             "bob@example.com",
		"Bob <Bob@Example.com>":       "bob@example.com",
		`"Bob, Jr" <bob@example.com>`: "bob@example.com",
		"Bad (( <bob@example.com>":    "bob@example.com",
	} {
		if got := addressKey(in); got != want {
			t.Errorf("addressKey(%q) = %q, want %q", in, got, want)
		}
	}
}
