package customize

import (
	"strings"
	"testing"

	"github.com/ThomasHabets/cmdg/pkg/input"
)

// Upstream moves keys around from time to time; these tests pin the
// two properties that make that safe to merge — that a keypress is
// translated in one step rather than chased through the table, and
// that the help text is derived from upstream's rather than replacing
// it.

func TestOpenMessageKeyDoesNotCascade(t *testing.T) {
	for _, tc := range []struct {
		press, want string
		why         string
	}{
		{"j", "n", "scroll down"},
		{"k", "p", "scroll up"},
		{"f", " ", "page down, onto the key forward vacated"},
		{"w", "f", "forward, moved clear of page down"},
		{"d", "d", "rebound to itself, so unchanged"},
		{"r", "r", "not customized at all"},
	} {
		if got := OpenMessageKey(tc.press); got != tc.want {
			t.Errorf("OpenMessageKey(%q) = %q, want %q (%s)",
				tc.press, got, tc.want, tc.why)
		}
	}
}

func TestOpenMessageKeyUnbindsDisplacedKeys(t *testing.T) {
	// A key whose upstream meaning moved elsewhere has to stop
	// doing anything, rather than keep doing the old thing.
	for _, press := range []string{"u", "n", "p", " ", input.Backspace} {
		if got := OpenMessageKey(press); got != unbound {
			t.Errorf("OpenMessageKey(%q) = %q, want unbound",
				press, got)
		}
	}
}

const upstreamOpenMessageHelp = `?, F1     — Help
u, ←           — Exit message
n, Down        — Scroll down
space          — Page down
f              — Forward message

Press [enter] to exit
`

func TestOpenMessageHelp(t *testing.T) {
	const want = `Help:            ?, F1
Exit message:    ESC, ←
Scroll down:     j, Down
Page down:       f
Forward message: w

Press [ESC] to exit
`
	if got := OpenMessageHelp(upstreamOpenMessageHelp); got != want {
		t.Errorf("OpenMessageHelp() =\n%s\nwant\n%s", got, want)
	}
}

func TestMessageListHelpKeepsUpstreamLayout(t *testing.T) {
	// Nothing is rebound in the message list, so its help text must
	// come back byte-identical apart from the key that dismisses
	// it. In particular upstream's own "u" must survive: "u" is
	// rebound in the open-message view, not in this one.
	const upstream = `?, F1              — Help
u                  — Unmark all messages

Press [enter] to exit
`
	want := strings.Replace(upstream, "[enter]", "[ESC]", 1)
	if got := MessageListHelp(upstream); got != want {
		t.Errorf("MessageListHelp() =\n%s\nwant\n%s", got, want)
	}
}
