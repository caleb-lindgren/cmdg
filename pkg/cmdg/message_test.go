package cmdg

import (
	"context"
	"reflect"
	"testing"

	gmail "google.golang.org/api/gmail/v1"
)

func TestFilteredEmails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		from    string
		self    []string
		headers []string
		want    []string
	}{
		{
			name: "self dropped from a multi-address To",
			from: "Alice <alice@example.com>",
			self: []string{"me@example.com"},
			headers: []string{
				"Me <me@example.com>, Bob <bob@example.com>",
			},
			want: []string{`"Bob" <bob@example.com>`},
		},
		{
			name: "self matched case-insensitively",
			from: "alice@example.com",
			self: []string{"me@example.com"},
			headers: []string{
				"ME@Example.com",
				"carol@example.com",
			},
			want: []string{"carol@example.com"},
		},
		{
			name: "sender and duplicates dropped, order kept",
			from: "alice@example.com",
			headers: []string{
				"bob@example.com, alice@example.com",
				"Bob <bob@example.com>, carol@example.com",
			},
			want: []string{"bob@example.com", "carol@example.com"},
		},
		{
			name:    "unparseable header kept verbatim",
			from:    "alice@example.com",
			headers: []string{"not an address"},
			want:    []string{"not an address"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := filteredEmails(tc.from, tc.self, tc.headers)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// headerMessage returns a message with the labels and headers given, as
// loaded at LevelMetadata, so that nothing is fetched. Header names are
// lowercase, as load stores them.
func headerMessage(labels []string, headers map[string]string) *Message {
	return &Message{
		ID:       "m1",
		Response: &gmail.Message{LabelIds: labels},
		level:    LevelMetadata,
		headers:  headers,
	}
}

func TestReplyRecipients(t *testing.T) {
	for _, tc := range []struct {
		name    string
		labels  []string
		headers map[string]string
		wantTo  string
	}{
		{
			name:   "received: the sender",
			labels: []string{Inbox},
			headers: map[string]string{
				"from": "Alice <alice@example.com>",
				"to":   "Me <me@example.com>",
			},
			wantTo: "Alice <alice@example.com>",
		},
		{
			name:   "received: Reply-To over From",
			labels: []string{Inbox},
			headers: map[string]string{
				"from":     "Alice <alice@example.com>",
				"reply-to": "list@example.com",
			},
			wantTo: "list@example.com",
		},
		{
			name:   "sent: whom it was sent to, not the user",
			labels: []string{Sent},
			headers: map[string]string{
				"from": "Me <me@example.com>",
				"to":   "Al <a@example.com>, b@example.com",
				"cc":   "carol@example.com",
			},
			wantTo: "Al <a@example.com>, b@example.com",
		},
		{
			name:   "sent: own Reply-To ignored",
			labels: []string{Sent, Inbox},
			headers: map[string]string{
				"from":     "Me <me@example.com>",
				"reply-to": "me@example.com",
				"to":       "alice@example.com",
			},
			wantTo: "alice@example.com",
		},
		{
			name:   "sent only to BCC: empty To",
			labels: []string{Sent},
			headers: map[string]string{
				"from": "Me <me@example.com>",
				"bcc":  "alice@example.com",
			},
			wantTo: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := headerMessage(tc.labels, tc.headers)
			got, err := msg.GetReplyTo(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.wantTo {
				t.Errorf("got To %q, want %q", got, tc.wantTo)
			}
		})
	}
}

// TestReplyAllToSent checks that replying to all of a message the user
// sent goes to its To and CC, without adding the user, as From would.
func TestReplyAllToSent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		wantTo  string
		wantCC  string
	}{
		{
			name: "To and CC",
			headers: map[string]string{
				"from": "Me <me@example.com>",
				"to":   "alice@example.com, bob@example.com",
				"cc":   "Carol <carol@example.com>",
				"bcc":  "dave@example.com",
			},
			wantTo: "alice@example.com, bob@example.com",
			wantCC: "Carol <carol@example.com>",
		},
		{
			name: "no CC",
			headers: map[string]string{
				"from": "Me <me@example.com>",
				"to":   "alice@example.com",
			},
			wantTo: "alice@example.com",
			wantCC: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := headerMessage([]string{Sent}, tc.headers)
			to, cc, err := msg.GetReplyToAll(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if to != tc.wantTo || cc != tc.wantCC {
				t.Errorf("got To %q CC %q, want To %q CC %q",
					to, cc, tc.wantTo, tc.wantCC)
			}
		})
	}
}
