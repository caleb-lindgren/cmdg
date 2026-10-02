package dialog

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ThomasHabets/cmdg/pkg/display"
	"github.com/ThomasHabets/cmdg/pkg/input"
)

// chars returns the keys that typing s presses, one per rune.
func chars(s string) []string {
	var ret []string
	for _, r := range s {
		ret = append(ret, string(r))
	}
	return ret
}

// paste returns the key the input loop sends for pasting s.
func paste(s string) []string {
	return []string{input.PasteStart + s}
}

// press draws the pane and then hands it each key, the way Addresses does,
// returning whether the last key submitted.
func press(p *addrPane, keyGroups ...[]string) bool {
	screen := display.NewScreen2(80, 12)
	submitted := false
	for _, keys := range keyGroups {
		for _, k := range keys {
			p.draw(screen)
			submitted = p.key(k)
		}
	}
	return submitted
}

func testPane() *addrPane {
	return newAddrPane(Strings2Options([]string{
		"alice@example.com",
		"bob@example.com",
		`"Smith, Carol" <carol@example.com>`,
	}))
}

// lineString returns an address line's text, with | where the cursor is.
func lineString(l *addrLine) string {
	return string(l.text[:l.cursor]) + "|" + string(l.text[l.cursor:])
}

func TestAddrPaneEditing(t *testing.T) {
	left := []string{input.Left}
	for _, test := range []struct {
		name string
		keys [][]string
		want string // The To line.
	}{
		{"typing", [][]string{chars("ali")}, "ali|"},
		{"comma starts a new address", [][]string{chars("a@x.com,bo")},
			"a@x.com, bo|"},
		{"space after comma dropped", [][]string{chars("a@x.com, bo")},
			"a@x.com, bo|"},
		{"comma after only whitespace", [][]string{
			chars("a@x.com,"), left, chars(",")},
			"a@x.com, |"},
		{"comma on empty line", [][]string{chars(",")}, "|"},
		{"comma inside quotes", [][]string{chars(`"Smith, J`)},
			`"Smith, J|`},
		{"escaped quote stays quoted", [][]string{chars(`"a\", b`)},
			`"a\", b|`},
		{"comma after closed quote", [][]string{chars(`"a" <a@x>,b`)},
			`"a" <a@x>, b|`},
		{"comma mid-address splits it", [][]string{
			chars("alicebob"), left, left, left, chars(",")},
			"alice, |bob"},
		{"backspace and arrows", [][]string{
			chars("abc"), left, left, {input.Backspace}},
			"|bc"},
		{"home, end, delete", [][]string{
			chars("abc"), {input.Home, input.Delete, input.End},
			chars("d")},
			"bcd|"},
		{"ctrl-u clears", [][]string{chars("abc"), {input.CtrlU}},
			"|"},
		{"control keys not inserted", [][]string{
			chars("a"), {input.F1, input.Return}},
			"a|"},
		{"enter on a result completes", [][]string{
			chars("ali"), {input.Down, input.Enter}},
			"alice@example.com|"},
		{"enter on a result mid-list", [][]string{
			chars("a@x, bo, c@z"), left, left, left, left, left,
			{input.Down, input.Enter}},
			"a@x, bob@example.com|, c@z"},
		{"paste a list", [][]string{
			paste("a@x.com\n\tb@y.com,   c\r\n")},
			"a@x.com, b@y.com, c, |"},
		{"paste with leading separators", [][]string{
			paste(",\n\t a@x.com")},
			"a@x.com|"},
		{"paste only separators", [][]string{paste(" ,\n\t")}, "|"},
		{"paste onto typed text", [][]string{
			chars("bob"), paste("@y.com, c@z")},
			"bob@y.com, c@z|"},
		{"paste before text", [][]string{
			chars("c@z"), {input.Home}, paste("a@x\nb@y\n")},
			"a@x, b@y, |c@z"},
		{"pasted tab inside quotes", [][]string{
			paste("\"a\tb\" <a@x>")},
			"\"a b\" <a@x>|"},
		{"pasted newline inside quotes", [][]string{
			paste("\"Doe,\nJane\" <j@x>\nb@y")},
			"\"Doe, Jane\" <j@x>, b@y|"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := testPane()
			if press(p, test.keys...) {
				t.Errorf("submitted")
			}
			if got := lineString(&p.lines[0]); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestAddrPaneSearch(t *testing.T) {
	for _, test := range []struct {
		name     string
		keys     [][]string
		want     []string // Keys of the options found, if not nil.
		selected int
	}{
		{"typed", [][]string{chars("EXAMPLE")}, []string{
			"alice@example.com",
			"bob@example.com",
			`"Smith, Carol" <carol@example.com>`,
		}, -1},
		{"last address", [][]string{chars("alice@example.com, bo")},
			[]string{"bob@example.com"}, -1},
		{"pasted list", [][]string{paste("x@y\nali")},
			[]string{"alice@example.com"}, -1},
		{"quoted comma", [][]string{chars(`"smith, c`)},
			[]string{`"Smith, Carol" <carol@example.com>`}, -1},
		{"down", [][]string{chars("example"), {input.Down}},
			nil, 0},
		{"down stops at last", [][]string{
			chars("bob"), {input.Down, input.Down}}, nil, 0},
		{"up returns to line", [][]string{
			chars("bob"), {input.Down, input.Up}}, nil, -1},
		{"typing returns to line", [][]string{
			chars("example"), {input.Down}, chars("x")}, nil, -1},
		{"tab returns to line", [][]string{
			chars("bob"), {input.Down, input.Tab}}, nil, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := testPane()
			press(p, test.keys...)
			var got []string
			for _, o := range p.visible {
				got = append(got, o.Key)
			}
			if test.want != nil &&
				!reflect.DeepEqual(got, test.want) {
				t.Errorf("found %q, want %q", got, test.want)
			}
			if p.selected != test.selected {
				t.Errorf("selected %d, want %d", p.selected,
					test.selected)
			}
		})
	}
}

func TestAddrPaneEmptySearch(t *testing.T) {
	for _, keys := range [][]string{nil, chars("bob,"), paste("ali\n")} {
		p := testPane()
		press(p, keys)
		if len(p.visible) != 0 {
			t.Errorf("after %q found %d, want none", keys,
				len(p.visible))
		}
	}
}

func TestAddrPaneSubmit(t *testing.T) {
	p := testPane()
	submitted := press(p,
		chars("ali"), // Results showing, but the line is selected.
		[]string{input.Tab}, chars("bob, "),
		[]string{input.Tab}, paste(",\nc@z\n"),
		[]string{input.Tab, input.Enter})
	if !submitted {
		t.Fatalf("Enter on the To line did not submit")
	}
	if p.focus != 0 {
		t.Errorf("Tab from BCC went to line %d, want 0", p.focus)
	}
	var got []string
	for n := range p.lines {
		got = append(got, p.lines[n].value())
	}
	if want := []string{"ali", "bob", "c@z"}; !reflect.DeepEqual(got,
		want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAddrPaneBackTab(t *testing.T) {
	p := testPane()
	press(p, []string{input.BackTab})
	if p.focus != 2 {
		t.Errorf("BackTab from To went to line %d, want 2", p.focus)
	}
}

func TestSplitAddresses(t *testing.T) {
	for _, test := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" , \n\t", nil},
		{"a@x", []string{"a@x"}},
		{", a@x, ", []string{"a@x"}},
		{"a@x\tb@y\n\nc@z", []string{"a@x", "b@y", "c@z"}},
		{`"Smith, John" <j@x>, b@y`,
			[]string{`"Smith, John" <j@x>`, "b@y"}},
		{"\"Doe\nJane\" <j@x>", []string{"\"Doe\nJane\" <j@x>"}},
		{"Smith, John <j@x>", []string{"Smith", "John <j@x>"}},
		{"a@x; b@y", []string{"a@x; b@y"}},
	} {
		got := SplitAddresses(test.in)
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("For %q got %q, want %q", test.in, got,
				test.want)
		}
	}
}

func TestValidateEmails(t *testing.T) {
	for _, test := range []struct {
		in    string
		valid bool
	}{
		{"", true},
		{"foo@bar.com", true},
		{"me", true},
		{"Me, foo@bar.com", true},
		{"foo@bar.com, baz@qux.com", true},
		{"foo@bar.com, invalid", false},
		{"Smith, John <j@x>", false},
		{"foo@bar.com, ", true},
		{"  foo@bar.com  ", true},
	} {
		err := validateEmails(test.in)
		if (err == nil) != test.valid {
			t.Errorf("For %q got valid=%v, want valid=%v. "+
				"Error: %v", test.in, err == nil, test.valid,
				err)
		}
	}
}

func TestAddrLineScroll(t *testing.T) {
	l := addrLine{text: []rune(strings.Repeat("a", 100)), cursor: 100}
	start := l.scroll(20)
	w := display.StringWidth(string(l.text[start:l.cursor]))
	if w > 20 {
		t.Errorf("cursor %d columns past the first shown, want <= 20",
			w)
	}
	l.cursor = 5
	if got := l.scroll(20); got != 0 {
		t.Errorf("cursor near the start scrolled to %d, want 0", got)
	}
}
