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
		{"Outlook header block, without Original Message",
			"Caleb,\nFiles are large.\nMark\n\n" +
				"From: Lindgren, Caleb <c@example.edu>\n" +
				"Sent: Friday, August 28, 2026 9:54 AM\n" +
				"To: Gygi, Steven <s@example.edu>; Mark\n" +
				"Subject: New URL\n\nJust wanted",
			[][2]int{{4, 10}}},
		{"header block with the rule above it, bold names",
			"Yes.\n\n________________________________\n" +
				"*From:* Bob <b@example.com>\n" +
				"*Date:* Monday\n" +
				"*Subject:* Hi\n*To:* Alice\n\nHello",
			[][2]int{{2, 9}}},
		{"forwarded, not folded",
			"FYI\n\n---------- Forwarded message ---------\n" +
				"From: Bob <b@example.com>\nDate: Mon\n" +
				"Subject: Hi\nTo: Alice\n\nHello",
			nil},
		{"header block with nothing before it, forwarded",
			"From: Bob <b@example.com>\nSent: Mon\n" +
				"To: Alice\n\nHi",
			nil},
		{"From in a sentence, not a header block",
			"From: the start, we said\nso.\nTo: be clear", nil},
		{"Apple Mail, an unquoted message, to the end",
			"Fair comments!\n\nOn Sep 3, 2026, at 9:50 AM, " +
				"Gygi, Steven <s@example.edu> wrote:" +
				"\n\n\nHave you\n\nFrom: Lindgren, Caleb" +
				"\nSent: Mon\nTo: Gygi\n\nHi",
			[][2]int{{2, 12}}},
		{"Apple Mail attribution wrapped",
			"Yes.\nOn Sep 3, 2026, at 9:50 AM, Bob <\n" +
				"b@example.com> wrote:\nHello",
			[][2]int{{1, 4}}},
		{"wrote: in a sentence, not folded",
			"Here is what he wrote:\nnothing much", nil},
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
