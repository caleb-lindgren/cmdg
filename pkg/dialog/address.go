package dialog

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/ThomasHabets/cmdg/pkg/display"
	"github.com/ThomasHabets/cmdg/pkg/input"
)

const (
	// addrSeparator is what goes between addresses on an address line.
	addrSeparator = ", "

	addrPaneRow    = 2
	addrPanePrefix = "    "
)

// addrLabels are the address lines of the address pane, top to bottom.
var addrLabels = [...]string{"To:  ", "CC:  ", "BCC: "}

// isAddrSeparator reports whether r separates addresses when not quoted.
func isAddrSeparator(r rune) bool {
	return r == ',' || r == '\t' || r == '\n' || r == '\r'
}

// addrSeparators returns the positions in s of the commas, tabs and newlines
// outside quotes, which separate one address from the next. Inside quotes a
// backslash escapes the next rune.
func addrSeparators(s []rune) []int {
	var ret []int
	quoted, escaped := false, false
	for n, r := range s {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case !quoted && isAddrSeparator(r):
			ret = append(ret, n)
		}
	}
	return ret
}

// inQuote reports whether a rune inserted at pos in s would be inside an
// unclosed quote.
func inQuote(s []rune, pos int) bool {
	quoted, escaped := false, false
	for _, r := range s[:pos] {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		}
	}
	return quoted
}

// splitAddrs splits s at its address separators, keeping the whitespace
// around each address.
func splitAddrs(s []rune) []string {
	var ret []string
	start := 0
	for _, p := range addrSeparators(s) {
		ret = append(ret, string(s[start:p]))
		start = p + 1
	}
	return append(ret, string(s[start:]))
}

// SplitAddresses splits an address list separated by commas, tabs or
// newlines into its addresses, trimmed, leaving out empty ones. Inside quotes
// these are part of a name, not separators.
func SplitAddresses(s string) []string {
	var ret []string
	for _, a := range splitAddrs([]rune(s)) {
		if a = strings.TrimSpace(a); a != "" {
			ret = append(ret, a)
		}
	}
	return ret
}

// normalizeAddrs rewrites an address list as addresses joined by
// addrSeparator. Empty addresses are dropped, except a last one, which is
// what is being typed: then s had a trailing separator and keeps one. The
// last address keeps its trailing whitespace, which may be the space before
// the next word of a name. A tab or newline inside quotes becomes a space.
func normalizeAddrs(s []rune) string {
	as := splitAddrs(s)
	var out []string
	for n, a := range as {
		a = strings.Map(func(r rune) rune {
			if isAddrSeparator(r) && r != ',' {
				return ' '
			}
			return r
		}, a)
		a = strings.TrimLeftFunc(a, unicode.IsSpace)
		if n < len(as)-1 {
			a = strings.TrimRightFunc(a, unicode.IsSpace)
			if a == "" {
				continue
			}
		}
		out = append(out, a)
	}
	if len(out) == 1 && out[0] == "" {
		// Only separators and whitespace.
		return ""
	}
	return strings.Join(out, addrSeparator)
}

// validateEmails checks that each address in s is "me" or has an @.
func validateEmails(s string) error {
	for _, a := range SplitAddresses(s) {
		if strings.EqualFold(a, "me") {
			// Special case shortcut.
			continue
		}
		if !strings.Contains(a, "@") {
			return fmt.Errorf("invalid email address: %q", a)
		}
	}
	return nil
}

// addrLine is one address line being edited, with the cursor before
// text[cursor].
type addrLine struct {
	text   []rune
	cursor int
}

// token returns the bounds of the address the cursor is in, from just after
// the separator before it to just before the separator after it.
func (l *addrLine) token() (int, int) {
	start, end := 0, len(l.text)
	for _, p := range addrSeparators(l.text) {
		if p < l.cursor {
			start = p + 1
		} else {
			end = p
			break
		}
	}
	return start, end
}

// head returns the line up to the address starting at start, with the
// separator before that address written as addrSeparator.
func (l *addrLine) head(start int) string {
	if start == 0 {
		return ""
	}
	return string(l.text[:start-1]) + addrSeparator
}

// set replaces the line with head followed by tail, with the cursor between.
func (l *addrLine) set(head, tail string) {
	l.text = []rune(head + tail)
	l.cursor = len([]rune(head))
}

// query returns what to search for: the address the cursor is in.
func (l *addrLine) query() string {
	s, e := l.token()
	return strings.TrimSpace(string(l.text[s:e]))
}

// value returns the addresses on the line, without empty ones, joined by
// addrSeparator.
func (l *addrLine) value() string {
	return strings.Join(SplitAddresses(string(l.text)), addrSeparator)
}

func (l *addrLine) insert(s string) {
	l.set(string(l.text[:l.cursor])+s, string(l.text[l.cursor:]))
}

// comma starts a new address after the cursor, unless the cursor is inside
// quotes, where a comma is part of a name. What is before the cursor in the
// address it is in stays as the address before the new one, unless it is
// only whitespace, in which case it is dropped. What is after the cursor
// starts the new address.
func (l *addrLine) comma() {
	if inQuote(l.text, l.cursor) {
		l.insert(",")
		return
	}
	s, _ := l.token()
	before := strings.TrimSpace(string(l.text[s:l.cursor]))
	after := strings.TrimLeftFunc(string(l.text[l.cursor:]),
		unicode.IsSpace)
	head := l.head(s)
	if before != "" {
		head += before + addrSeparator
	}
	l.set(head, after)
}

// paste inserts pasted text at the cursor, rewriting the line up to the end
// of it as a list joined by addrSeparator.
func (l *addrLine) paste(s string) {
	var b strings.Builder
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' || unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	head := normalizeAddrs([]rune(string(l.text[:l.cursor]) + b.String()))
	tail := string(l.text[l.cursor:])
	if strings.HasSuffix(head, addrSeparator) {
		tail = strings.TrimLeftFunc(tail, unicode.IsSpace)
	}
	l.set(head, tail)
}

// complete replaces the address the cursor is in with addr.
func (l *addrLine) complete(addr string) {
	s, e := l.token()
	l.set(l.head(s)+addr, string(l.text[e:]))
}

func (l *addrLine) backspace() {
	if l.cursor > 0 {
		l.set(string(l.text[:l.cursor-1]), string(l.text[l.cursor:]))
	}
}

func (l *addrLine) del() {
	if l.cursor < len(l.text) {
		l.set(string(l.text[:l.cursor]), string(l.text[l.cursor+1:]))
	}
}

// scroll returns the first rune to show of the line, for the cursor to be
// within width columns of it.
func (l *addrLine) scroll(width int) int {
	start := 0
	for start < l.cursor &&
		display.StringWidth(string(l.text[start:l.cursor])) > width {
		start++
	}
	return start
}

// leadingSpace reports whether key is whitespace typed before anything else
// in the address the cursor is in, which is dropped like whitespace after a
// separator in a pasted list.
func (l *addrLine) leadingSpace(key string) bool {
	s, _ := l.token()
	return strings.TrimSpace(key) == "" &&
		strings.TrimSpace(string(l.text[s:l.cursor])) == ""
}

// isPrintable reports whether a key is text to insert, not a control key.
func isPrintable(key string) bool {
	for _, r := range key {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return key != ""
}

// addrPane is the state of the Addresses dialog.
type addrPane struct {
	opts     []*Option
	lines    [len(addrLabels)]addrLine
	focus    int
	visible  []*Option // Options matching the focused line's query.
	shown    int       // How many of visible were drawn.
	selected int       // Index into visible, or -1 for the line itself.
	searched string    // focus and query that visible is for.
}

func newAddrPane(opts []*Option) *addrPane {
	p := &addrPane{opts: opts, selected: -1, searched: "-"}
	p.refresh()
	return p
}

// refresh searches again if the focus or what to search for has changed,
// returning from the results to the line.
func (p *addrPane) refresh() {
	q := p.lines[p.focus].query()
	if s := fmt.Sprintf("%d %s", p.focus, q); s != p.searched {
		p.searched = s
		p.selected = -1
		p.visible = nil
		if q != "" {
			p.visible = filterSubmatch(p.opts, q)
		}
	}
}

// draw draws the address lines, then the options that fit below them.
func (p *addrPane) draw(screen *display.Screen) {
	width := max(screen.Width-
		display.StringWidth(addrPanePrefix+addrLabels[0])-1, 1)
	curX := 0
	for n := range p.lines {
		l := &p.lines[n]
		label := addrPanePrefix + addrLabels[n]
		start := 0
		if n == p.focus {
			label = addrPanePrefix + display.Bold + addrLabels[n] +
				display.Reset
			start = l.scroll(width)
			curX = display.StringWidth(label+
				string(l.text[start:l.cursor])) + 1
		}
		screen.Printlnf(addrPaneRow+n, "%s%s", label,
			string(l.text[start:]))
	}
	optRow := addrPaneRow + len(p.lines) + 1
	screen.Printlnf(optRow-1, "")
	p.shown = drawOptions(screen, optRow, addrPanePrefix, p.visible,
		p.selected)
	screen.SetCursor(addrPaneRow+p.focus, curX)
}

// key handles one key, returning true if it submits the addresses.
func (p *addrPane) key(key string) bool {
	l := &p.lines[p.focus]
	switch key {
	case input.Enter:
		if p.selected < 0 {
			return true
		}
		l.complete(p.visible[p.selected].Key)
		p.selected = -1
	case input.CtrlN, input.Down:
		if p.selected < p.shown-1 {
			p.selected++
		}
	case input.CtrlP, input.Up:
		if p.selected >= 0 {
			p.selected--
		}
	case input.Tab:
		p.focus = (p.focus + 1) % len(p.lines)
	case input.BackTab:
		p.focus = (p.focus + len(p.lines) - 1) % len(p.lines)
	case input.Left:
		l.cursor = max(l.cursor-1, 0)
	case input.Right:
		l.cursor = min(l.cursor+1, len(l.text))
	case input.Home, input.XHome, input.CtrlA:
		l.cursor = 0
	case input.End, input.XEnd, input.CtrlE:
		l.cursor = len(l.text)
	case input.Backspace, input.CtrlH:
		l.backspace()
	case input.Delete:
		l.del()
	case input.CtrlU:
		*l = addrLine{}
	case ",":
		l.comma()
	default:
		if pasted, ok := strings.CutPrefix(key, input.PasteStart); ok {
			l.paste(pasted)
		} else if isPrintable(key) && !l.leadingSpace(key) {
			l.insert(key)
		}
	}
	p.refresh()
	return false
}

// Addresses asks for the To, CC and BCC addresses of a message, on three
// lines that Tab moves between. Each searches opts for the address the
// cursor is in, and Down moves into what it found. Enter on a search result
// puts it in the line; Enter on a line returns all three lines, as addresses
// joined by ", ".
func Addresses(opts []*Option, keys *input.Input) (to, cc, bcc string,
	err error) {
	screen, err := display.NewScreen()
	if err != nil {
		return "", "", "", err
	}
	keys.PastePush(false)
	defer keys.PastePop()
	fmt.Print(display.BracketedPasteOn)
	defer fmt.Print(display.BracketedPasteOff)

	p := newAddrPane(opts)
	for {
		p.draw(screen)
		screen.Draw()
		key := <-keys.Chan()
		if key == input.CtrlC {
			return "", "", "", ErrAborted
		}
		if !p.key(key) {
			continue
		}
		var vals [len(addrLabels)]string
		bad := -1
		for n := range p.lines {
			vals[n] = p.lines[n].value()
			err := validateEmails(vals[n])
			if err == nil || bad >= 0 {
				continue
			}
			bad = n
			label := strings.TrimSpace(addrLabels[n])
			_ = Message("Invalid recipients",
				label+" "+err.Error(), keys)
		}
		if bad < 0 {
			return vals[0], vals[1], vals[2], nil
		}
		p.focus = bad
		p.refresh()
	}
}
