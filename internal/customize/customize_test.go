package customize

import (
	"testing"

	"github.com/ThomasHabets/cmdg/pkg/input"
)

// Upstream moves keys around from time to time; these tests pin the
// properties that make that safe to merge — that a keypress is
// translated in one step rather than chased through the table, and
// that the help text is derived from upstream's rather than replacing
// it.
//
// The key tests build their own translator instead of calling
// OpenMessageKey, so that a half-typed chord in one test cannot leak
// into the next.

func TestTranslateDoesNotCascade(t *testing.T) {
	tr := newTranslator(openMessageBindings)
	for _, tc := range []struct {
		press, want string
		why         string
	}{
		{"j", "n", "scroll down"},
		{"k", "p", "scroll up"},
		{"f", " ", "page down, onto the key forward vacated"},
		{"w", "f", "forward, moved clear of page down"},
		{"/", "s", "search"},
		{"d", "d", "rebound to itself, so unchanged"},
		{"r", "r", "not customized at all"},
		{input.Home, input.Home, "aliased by gg, so still works"},
		{ctrlD, HalfPageDown, "half page down"},
		{input.CtrlU, HalfPageUp, "half page up"},
	} {
		if got := tr.translate(tc.press); got != tc.want {
			t.Errorf("translate(%q) = %q, want %q (%s)",
				tc.press, got, tc.want, tc.why)
		}
	}
}

func TestTranslateUnbindsDisplacedKeys(t *testing.T) {
	// A key whose upstream meaning moved elsewhere has to stop
	// doing anything, rather than keep doing the old thing.
	tr := newTranslator(openMessageBindings)
	for _, press := range []string{
		"u", "n", "p", " ", "s", input.Backspace,
	} {
		if got := tr.translate(press); got != unbound {
			t.Errorf("translate(%q) = %q, want unbound",
				press, got)
		}
	}
}

func TestChords(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bs    []Binding
		press []string
		want  string
	}{
		{"gg goes to the top", openMessageBindings,
			[]string{"g", "g"}, input.Home},
		{"G goes to the bottom", openMessageBindings,
			[]string{"G"}, GoBottom},
		{"a lone g does nothing yet", openMessageBindings,
			[]string{"g"}, unbound},
		{"a broken chord swallows both keys", openMessageBindings,
			[]string{"g", "r"}, unbound},
		{"gl still reaches go-to-label", messageListBindings,
			[]string{"g", "l"}, "g"},
		{"gg works in the list too", messageListBindings,
			[]string{"g", "g"}, input.Home},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTranslator(tc.bs)
			var got string
			for _, k := range tc.press {
				got = tr.translate(k)
			}
			if got != tc.want {
				t.Errorf("translate(%q) = %q, want %q",
					tc.press, got, tc.want)
			}
		})
	}
}

func TestExportedKeyFuncs(t *testing.T) {
	// Both chords are completed, so no pending state is left over.
	if got := OpenMessageKey("/"); got != "s" {
		t.Errorf(`OpenMessageKey("/") = %q, want "s"`, got)
	}
	if got := MessageListKey("/"); got != "s" {
		t.Errorf(`MessageListKey("/") = %q, want "s"`, got)
	}
}

func TestOpenMessageHelp(t *testing.T) {
	const upstream = `?, F1     — Help
u, ←           — Exit message
n, Down        — Scroll down
space          — Page down
f              — Forward message
s, ^s          — Search within message

Press [enter] to exit
`
	const want = `Help:                  ?, F1
Exit message:          ESC, ←
Scroll down:           j, Down
Page down:             f
Forward message:       w
Search within message: /, ^s
Half page down:        ^D
Half page up:          ^U
Go to top:             gg
Go to bottom:          G

Press [ESC] to exit
`
	if got := OpenMessageHelp(upstream); got != want {
		t.Errorf("OpenMessageHelp() =\n%s\nwant\n%s", got, want)
	}
}

func TestMessageListHelpKeepsUpstreamLayout(t *testing.T) {
	// The list keeps upstream's key-first columns, so its alignment
	// has to survive both the rename of "g" to "gl" and the two
	// added lines. Upstream's own "u" must survive untouched too:
	// "u" is rebound in the open-message view, not in this one.
	const upstream = `?, F1              — Help
u                  — Unmark all messages
g                  — Go to label
s, ^s              — Search

Press [enter] to exit
`
	const want = `?, F1              — Help
u                  — Unmark all messages
gl                 — Go to label
/, ^s              — Search
^D                 — Half page down
^U                 — Half page up
gg                 — Go to top
G                  — Go to bottom (of what is loaded)

Press [ESC] to exit
`
	if got := MessageListHelp(upstream); got != want {
		t.Errorf("MessageListHelp() =\n%s\nwant\n%s", got, want)
	}
}
