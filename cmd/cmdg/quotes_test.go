package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestQuoteBlocks(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want [][2]int
	}{
		{"none", "Hello\nthere", nil},
		{"top posted, attribution",
			"Sounds good.\n\nOn Mon, 5 Oct 2026, Alice wrote:\n" +
				"> Lunch?\n>\n> A",
			[][2]int{{2, 6}}},
		{"attribution wrapped by Gmail",
			"Yes.\n\nOn Mon, 5 Oct 2026 at 10:00, Alice <\n" +
				"alice@example.com> wrote:\n\n> Lunch?",
			[][2]int{{2, 6}}},
		{"blank lines inside a quote, not after it",
			"> one\n\n> two\n\nBye", [][2]int{{0, 3}}},
		{"inline replies, each quote folded",
			"Alice writes:\n> one?\nYes.\n> two?\nNo.",
			[][2]int{{0, 2}, {3, 4}}},
		{"nested quotes are one quote", "> a\n>> b\n> c",
			[][2]int{{0, 3}}},
		{"indented quote", "Hi\n  > a", [][2]int{{1, 2}}},
		{"Outlook, to the end",
			"Fine.\n\n-----Original Message-----\nFrom: Bob\n" +
				"Sent: Monday\n\nHello",
			[][2]int{{2, 7}}},
		{"a colon that is not an attribution",
			"Two things:\n> a", [][2]int{{1, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := quoteBlocks(strings.Split(tc.body, "\n"))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFoldQuotes(t *testing.T) {
	in := strings.Split("Sounds good.\n\nAlice wrote:\n> a\n> b\nBye",
		"\n")
	got := foldQuotes(in)
	want := []string{"Sounds good.", "", foldLine(3), "Bye"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if !strings.Contains(foldLine(1), "1 quoted line,") {
		t.Errorf("foldLine(1) = %q, want singular", foldLine(1))
	}
}

func TestWrapLine(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  []string
	}{
		{"", 3, []string{""}},
		{"abc", 3, []string{"abc"}},
		{"abcdefg", 3, []string{"abc", "def", "g"}},
		// Two columns each; a third would not fit in 5.
		{"日本語です", 5, []string{"日本", "語で", "す"}},
		// Colours take no columns.
		{"\x1b[1mab\x1b[38;5;7mcd\x1b[0m", 3,
			[]string{"\x1b[1mab\x1b[38;5;7mc", "d\x1b[0m"}},
	} {
		if got := wrapLine(tc.in, tc.width); !reflect.DeepEqual(got,
			tc.want) {
			t.Errorf("wrapLine(%q, %d) = %q, want %q", tc.in,
				tc.width, got, tc.want)
		}
	}
}
