package cmdg

import (
	"reflect"
	"testing"
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
