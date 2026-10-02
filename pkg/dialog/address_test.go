package dialog

import (
	"fmt"
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

// repeat returns n presses of key.
func repeat(key string, n int) []string {
	var ret []string
	for range n {
		ret = append(ret, key)
	}
	return ret
}

func lefts(n int) []string {
	return repeat(input.Left, n)
}

func rights(n int) []string {
	return repeat(input.Right, n)
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
			chars("a@x, bob, c@z"), left, left, left, left, left,
			{input.Backspace, input.Down, input.Enter}},
			"a@x, bob@example.com|, c@z"},
		{"semicolon starts a new address", [][]string{
			chars("a@x.com;bo")},
			"a@x.com, bo|"},
		{"semicolon inside quotes", [][]string{chars(`"Smith; J`)},
			`"Smith; J|`},
		{"paste semicolons", [][]string{paste("a@x; b@y;\nc@z")},
			"a@x, b@y, c@z|"},
		{"paste quoted semicolon", [][]string{
			paste(`"Doe; J" <j@x>;b@y`)},
			`"Doe; J" <j@x>, b@y|`},
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
		{"changed after moving to a previous address", [][]string{
			chars("bob, ali"), lefts(5), {input.Backspace}},
			[]string{"bob@example.com"}, -1},
		{"moved back to the address changed", [][]string{
			chars("bob, ali"), lefts(5), rights(5)},
			[]string{"alice@example.com"}, -1},
		{"moving within the address keeps results", [][]string{
			chars("ali"), {input.Down}, {input.Home, input.End}},
			[]string{"alice@example.com"}, 0},
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
	for _, keys := range [][][]string{
		nil,
		{chars("bob,")},
		{paste("ali\n")},
		// Moved into a previous address without changing it.
		{chars("bob, ali"), lefts(5)},
		{chars("bob, ali"), {input.Home}},
		{paste("bob\nali"), {input.Home, input.Right}},
	} {
		p := testPane()
		press(p, keys...)
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
		{"a@x; b@y", []string{"a@x", "b@y"}},
		{`"Doe; J" <j@x>; b@y`, []string{`"Doe; J" <j@x>`, "b@y"}},
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
		{"foo@bar.com; baz@qux.com", true},
		{"invalid; foo@bar.com", false},
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

var esc = []string{input.Esc}

func TestAddrPaneViEditing(t *testing.T) {
	for _, test := range []struct {
		name   string
		keys   [][]string
		want   string // The To line.
		normal bool
	}{
		{"esc moves left", [][]string{chars("abc"), esc}, "ab|c",
			true},
		{"0", [][]string{chars("abcd"), esc, {"0"}}, "|abcd", true},
		{"$", [][]string{chars("abcd"), esc, {"0", "$"}}, "abc|d",
			true},
		{"l stops at the last", [][]string{chars("ab"), esc,
			{"l", "l"}}, "a|b", true},
		{"h", [][]string{chars("abc"), esc, {"h"}}, "a|bc", true},
		// These type an address that matches no contact: with
		// results shown, b and d would move through them instead.
		{"w", [][]string{chars("zed@site.com"), esc, {"0", "w"}},
			"zed|@site.com", true},
		{"ww", [][]string{
			chars("zed@site.com"), esc, {"0", "w", "w"}},
			"zed@|site.com", true},
		{"b", [][]string{
			chars("zed@site.com"), esc, {"0", "w", "w", "b"}},
			"zed|@site.com", true},
		{"w at the end", [][]string{chars("a@x"), esc, {"0", "w", "w",
			"w", "w"}}, "a@|x", true},
		{"W", [][]string{chars("a@x, b@y"), esc, {"0", "W"}},
			"a@x, |b@y", true},
		{"B", [][]string{chars("a@x, b@y"), esc, {"B"}},
			"a@x, |b@y", true},
		{"x", [][]string{chars("abc"), esc, {"x"}}, "a|b", true},
		{"x on empty line", [][]string{esc, {"x"}}, "|", true},
		{"r", [][]string{chars("abc"), esc, {"0", "r", "z"}}, "|zbc",
			true},
		{"dw", [][]string{
			chars("zed@site.com"), esc, {"0", "d", "w"}},
			"|@site.com", true},
		{"db", [][]string{chars("foo bar"), esc, {"d", "b"}},
			"foo |r", true},
		{"dh", [][]string{chars("abc"), esc, {"d", "h"}}, "a|c", true},
		{"dd", [][]string{chars("abc"), esc, {"d", "d"}}, "|", true},
		{"D", [][]string{chars("abcdef"), esc, {"0", "l", "l", "D"}},
			"a|b", true},
		{"esc cancels d", [][]string{chars("abc"), esc,
			{"d", input.Esc, "x"}}, "a|b", true},
		{"unknown motion cancels d", [][]string{chars("abc"), esc,
			{"d", "z"}}, "ab|c", true},
		{"cw", [][]string{chars("zed@site.com"), esc,
			{"0", "c", "w"}, chars("bob")},
			"bob|@site.com", false},
		{"b with results shown moves through them", [][]string{
			chars("alice@example.com"), esc, {"b"}},
			"alice@example.co|m", true},
		{"cc", [][]string{chars("abc"), esc, {"c", "c"}, chars("x")},
			"x|", false},
		{"C", [][]string{chars("abcdef"), esc, {"0", "l", "C"},
			chars("z")}, "az|", false},
		{"i", [][]string{chars("bc"), esc, {"i"}, chars("x")}, "bx|c",
			false},
		{"a", [][]string{chars("bc"), esc, {"a"}, chars("x")}, "bcx|",
			false},
		{"I", [][]string{chars("bc"), esc, {"I"}, chars("x")}, "x|bc",
			false},
		{"A", [][]string{chars("bc"), esc, {"0", "A"}, chars("x")},
			"bcx|", false},
		{"meta key is esc then key", [][]string{chars("abc"),
			{"Meta-0"}}, "|abc", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := testPane()
			if press(p, test.keys...) {
				t.Errorf("submitted")
			}
			if got := lineString(&p.lines[0]); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
			if p.normal != test.normal {
				t.Errorf("normal mode %v, want %v", p.normal,
					test.normal)
			}
		})
	}
}

func TestAddrPaneViLines(t *testing.T) {
	for _, test := range []struct {
		keys  []string
		focus int
	}{
		{[]string{"j"}, 1},
		{[]string{"j", "j", "j"}, 2},
		{[]string{"j", "k"}, 0},
		{[]string{"k"}, 0},
		{[]string{"G"}, 2},
		{[]string{"G", "g", "g"}, 0},
		{[]string{input.Tab, input.Tab, input.Tab}, 0},
	} {
		p := testPane()
		press(p, esc, test.keys)
		if p.focus != test.focus || !p.normal {
			t.Errorf("after Esc %q on line %d, normal mode %v; "+
				"want line %d in normal mode", test.keys,
				p.focus, p.normal, test.focus)
		}
	}
	p := testPane()
	if !press(p, chars("a@x"), esc, []string{"j", input.Enter}) {
		t.Errorf("Enter in normal mode did not submit")
	}
}

// testManyPane returns a pane with more options than fit on its screen.
func testManyPane() *addrPane {
	var opts []string
	for n := range 30 {
		opts = append(opts, fmt.Sprintf("user%02d@example.com", n))
	}
	return newAddrPane(Strings2Options(opts))
}

func TestAddrPaneViBrowse(t *testing.T) {
	// press draws on 12 rows, which leaves 6 for options.
	for _, test := range []struct {
		name     string
		keys     []string
		selected int
		found    int
	}{
		{"esc keeps results", nil, -1, 30},
		{"j", []string{"j"}, 0, 30},
		{"jjj", []string{"j", "j", "j"}, 2, 30},
		{"k back to the line", []string{"j", "k"}, -1, 30},
		{"G", []string{"G"}, 29, 30},
		{"gg", []string{"G", "g", "g"}, 0, 30},
		{"f", []string{"f"}, 5, 30},
		{"ff", []string{"f", "f"}, 11, 30},
		{"b", []string{"f", "f", "b"}, 5, 30},
		{"b stops at the first", []string{"f", "b"}, 0, 30},
		{"b from the line", []string{"b"}, 0, 30},
		{"d", []string{"d"}, 2, 30},
		{"u", []string{"d", "d", "u"}, 2, 30},
		{"moving in the address keeps results", []string{"0"}, -1, 30},
		{"esc hides results", []string{input.Esc}, -1, 0},
		{"editing hides results", []string{"x"}, -1, 0},
		{"i searches again", []string{input.Esc, "i"}, -1, 30},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := testManyPane()
			press(p, chars("user"), esc, test.keys)
			if p.selected != test.selected {
				t.Errorf("selected %d, want %d", p.selected,
					test.selected)
			}
			if len(p.visible) != test.found {
				t.Errorf("found %d, want %d", len(p.visible),
					test.found)
			}
		})
	}

	p := testManyPane()
	press(p, chars("user"), esc, []string{input.Esc, "j"})
	if p.focus != 1 {
		t.Errorf("j with results hidden went to line %d, want 1",
			p.focus)
	}

	p = testManyPane()
	press(p, chars("user"), esc, []string{"G"})
	p.draw(display.NewScreen2(80, 12))
	if p.scroll != 24 {
		t.Errorf("selecting the last of 30 on 6 rows scrolled to %d, "+
			"want 24", p.scroll)
	}

	p = testManyPane()
	if press(p, chars("user"), esc, []string{"j", "j", input.Enter}) {
		t.Errorf("Enter on a result submitted")
	}
	if got, want := p.lines[0].value(), "user01@example.com"; got != want {
		t.Errorf("Enter on a result put %q in the line, want %q", got,
			want)
	}
	if !p.normal || len(p.visible) != 0 {
		t.Errorf("after Enter on a result normal mode %v, %d "+
			"found; want normal mode, none found", p.normal,
			len(p.visible))
	}

	p = testManyPane()
	if !press(p, chars("user"), esc, []string{input.Enter}) {
		t.Errorf("Enter on the line with results shown did not submit")
	}
}

func TestAddrPaneInsertScrolls(t *testing.T) {
	p := testManyPane()
	press(p, chars("user"), repeat(input.Down, 10))
	if p.selected != 9 {
		t.Errorf("10 Downs selected %d, want 9", p.selected)
	}
}

func TestWordMotions(t *testing.T) {
	s := []rune("alice@example.com, b c")
	for _, test := range []struct {
		name string
		got  int
		want int
	}{
		{"w in word", wordForward(s, 1, false), 5},
		{"w on punctuation", wordForward(s, 5, false), 6},
		{"w to separator", wordForward(s, 14, false), 17},
		{"w over separator", wordForward(s, 17, false), 19},
		{"W", wordForward(s, 0, true), 19},
		{"w at end", wordForward(s, len(s), false), len(s)},
		{"b to word start", wordBack(s, 3, false), 0},
		{"b from word start", wordBack(s, 6, false), 5},
		{"b over whitespace", wordBack(s, 21, false), 19},
		{"B", wordBack(s, 19, true), 0},
		{"e", wordEnd(s, 0, false), 5},
		{"e on whitespace", wordEnd(s, 18, false), 19},
	} {
		if test.got != test.want {
			t.Errorf("%s: got %d, want %d", test.name, test.got,
				test.want)
		}
	}
}
