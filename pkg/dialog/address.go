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
	return r == ',' || r == ';' || isLineSeparator(r)
}

// isLineSeparator reports whether r is a tab or newline, which separate
// addresses in a pasted list.
func isLineSeparator(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r'
}

// addrSeparators returns the positions in s of the commas, semicolons, tabs
// and newlines outside quotes, which separate one address from the next.
// Inside quotes a backslash escapes the next rune.
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

// SplitAddresses splits an address list separated by commas, semicolons,
// tabs or newlines into its addresses, trimmed, leaving out empty ones.
// Inside quotes these are part of a name, not separators.
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
			if isLineSeparator(r) {
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
	edited int // Index of the address last changed, as tokenIndex.
}

// tokenIndex returns which address on the line the cursor is in, counting
// from 0.
func (l *addrLine) tokenIndex() int {
	n := 0
	for _, p := range addrSeparators(l.text) {
		if p < l.cursor {
			n++
		}
	}
	return n
}

// searching reports whether to search for the address the cursor is in: it
// is the one last changed, not one the cursor was only moved into.
func (l *addrLine) searching() bool {
	return l.tokenIndex() == l.edited
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

// set replaces the line with head followed by tail, with the cursor between,
// which makes the address the cursor is in the one last changed.
func (l *addrLine) set(head, tail string) {
	l.text = []rune(head + tail)
	l.cursor = len([]rune(head))
	l.edited = l.tokenIndex()
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
// quotes, where a comma is part of a name. A semicolon does the same, and
// becomes a comma like other separators. What is before the cursor in the
// address it is in stays as the address before the new one, unless it is
// only whitespace, in which case it is dropped. What is after the cursor
// starts the new address.
func (l *addrLine) comma(sep string) {
	if inQuote(l.text, l.cursor) {
		l.insert(sep)
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

// complete replaces the address the cursor is in with addr. It is not
// searched for until changed, as it would find addr again, and Enter would
// put that in instead of submitting.
func (l *addrLine) complete(addr string) {
	s, e := l.token()
	l.set(l.head(s)+addr, string(l.text[e:]))
	l.edited = -1
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

// charClass groups runes the way vi's word motions do: 0 for whitespace, 1
// for letters, digits and underscores, and 2 for other punctuation. With
// big, as for W and B, everything but whitespace is 1.
func charClass(r rune, big bool) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case big, r == '_', unicode.IsLetter(r), unicode.IsDigit(r):
		return 1
	}
	return 2
}

// wordForward returns where vi's w moves from i: the start of the next word,
// or the end of s.
func wordForward(s []rune, i int, big bool) int {
	if i >= len(s) {
		return len(s)
	}
	c := charClass(s[i], big)
	for i < len(s) && c != 0 && charClass(s[i], big) == c {
		i++
	}
	for i < len(s) && charClass(s[i], big) == 0 {
		i++
	}
	return i
}

// wordBack returns where vi's b moves from i: the start of the word i is in,
// or of the one before if i is already at a start.
func wordBack(s []rune, i int, big bool) int {
	i = min(i, len(s))
	for i > 0 && charClass(s[i-1], big) == 0 {
		i--
	}
	if i == 0 {
		return 0
	}
	c := charClass(s[i-1], big)
	for i > 0 && charClass(s[i-1], big) == c {
		i--
	}
	return i
}

// wordEnd returns the end, exclusive, of the word at i, which is as far as
// vi's cw changes: unlike dw, not the whitespace after it. On whitespace it
// is where w moves.
func wordEnd(s []rune, i int, big bool) int {
	if i >= len(s) || charClass(s[i], big) == 0 {
		return wordForward(s, i, big)
	}
	c := charClass(s[i], big)
	for i < len(s) && charClass(s[i], big) == c {
		i++
	}
	return i
}

// clampNormal keeps the cursor on a character, as in vi's normal mode, where
// it cannot be after the last one.
func (l *addrLine) clampNormal() {
	if l.cursor >= len(l.text) {
		l.cursor = max(len(l.text)-1, 0)
	}
}

// deleteRange deletes text[a:b], or text[b:a], leaving the cursor at the
// start of it.
func (l *addrLine) deleteRange(a, b int) {
	a, b = max(min(a, b), 0), min(max(a, b), len(l.text))
	if a >= b {
		return
	}
	l.set(string(l.text[:a]), string(l.text[b:]))
}

// motion returns where a vi motion key moves the cursor, and false if key is
// not a motion. For an operator, $ is past the last character.
func (l *addrLine) motion(key string) (int, bool) {
	switch key {
	case "w", "W":
		return wordForward(l.text, l.cursor, key == "W"), true
	case "b", "B":
		return wordBack(l.text, l.cursor, key == "B"), true
	case "h", input.Left:
		return max(l.cursor-1, 0), true
	case "l", input.Right:
		return min(l.cursor+1, len(l.text)), true
	case "0", input.Home, input.XHome:
		return 0, true
	case "$", input.End, input.XEnd:
		return len(l.text), true
	}
	return 0, false
}

// addrPane is the state of the Addresses dialog.
type addrPane struct {
	opts     []*Option
	lines    [len(addrLabels)]addrLine
	focus    int
	visible  []*Option // Options matching the focused line's query.
	selected int       // Index into visible, or -1 if it is empty.
	scroll   int       // Index into visible of the first drawn.
	rows     int       // How many options fit on the screen.
	searched string    // focus and query that visible is for.

	// normal is vi's normal mode. In it, browsing is whether the search
	// results from before Esc are still shown, to be moved through like
	// less; changing the line or the address hides them. pending is an
	// operator key, such as d or r, waiting for the key it applies to.
	normal   bool
	browsing bool
	pending  string
}

func newAddrPane(opts []*Option) *addrPane {
	p := &addrPane{opts: opts, selected: -1, searched: "-"}
	p.refresh()
	return p
}

// refresh searches again if the focus or what to search for has changed,
// returning from the results to the line. Moving the cursor into an address
// other than the one last changed searches for nothing until it is changed.
// Normal mode does not search, and shows results only while browsing.
func (p *addrPane) refresh() {
	l := &p.lines[p.focus]
	q := ""
	if l.searching() {
		q = l.query()
	}
	if s := fmt.Sprintf("%d %s", p.focus, q); s != p.searched {
		p.searched = s
		p.selected = -1
		p.scroll = 0
		p.visible = nil
		p.browsing = false
		if q != "" && !p.normal {
			p.visible = filterSubmatch(p.opts, q)
		}
		if len(p.visible) > 0 {
			p.selected = 0
		}
	}
	if p.normal && !p.browsing {
		p.visible = nil
		p.selected = -1
	}
}

// draw draws the address lines, then the options that fit below them,
// scrolled to show the selected one.
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
	mode := ""
	if p.normal {
		mode = addrPanePrefix + "-- NORMAL --"
	}
	screen.Printlnf(optRow-1, "%s", mode)
	p.rows = max(screen.Height-optRow, 1)
	if p.selected < p.scroll {
		p.scroll = max(p.selected, 0)
	}
	if p.selected >= p.scroll+p.rows {
		p.scroll = p.selected - p.rows + 1
	}
	p.scroll = min(p.scroll, len(p.visible))
	drawOptions(screen, optRow, addrPanePrefix, p.visible[p.scroll:],
		p.selected-p.scroll)
	screen.SetCursor(addrPaneRow+p.focus, curX)
}

// moveSelection moves the selected option by n, stopping at the first and
// last.
func (p *addrPane) moveSelection(n int) {
	if len(p.visible) == 0 {
		return
	}
	p.selected = max(min(p.selected+n, len(p.visible)-1), 0)
}

// key handles one key, returning true if it submits the addresses. While
// search results are shown, Enter puts the selected one, at first the first,
// in the line, and only submits when none are shown.
func (p *addrPane) key(key string) bool {
	// Esc and a key typed within readKey's 10ms of it arrive as one.
	if k, ok := strings.CutPrefix(key, "Meta-"); ok {
		p.key(input.Esc)
		return p.key(k)
	}
	var submit bool
	if p.normal {
		submit = p.normalKey(key)
	} else {
		submit = p.insertKey(key)
	}
	if p.normal {
		p.lines[p.focus].clampNormal()
	}
	p.refresh()
	return submit
}

// insertKey handles a key in insert mode, which is the mode the pane starts
// in, returning true if it submits the addresses.
func (p *addrPane) insertKey(key string) bool {
	l := &p.lines[p.focus]
	switch key {
	case input.Enter:
		if len(p.visible) == 0 {
			return true
		}
		l.complete(p.visible[p.selected].Key)
	case input.Esc:
		p.normal = true
		p.browsing = len(p.visible) > 0
		l.cursor = max(l.cursor-1, 0)
	case input.CtrlN, input.Down:
		p.moveSelection(1)
	case input.CtrlP, input.Up:
		p.moveSelection(-1)
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
	case ",", ";":
		l.comma(key)
	default:
		if pasted, ok := strings.CutPrefix(key, input.PasteStart); ok {
			l.paste(pasted)
		} else if isPrintable(key) && !l.leadingSpace(key) {
			l.insert(key)
		}
	}
	return false
}

// insertMode leaves normal mode, with the cursor at pos.
func (p *addrPane) insertMode(pos int) {
	l := &p.lines[p.focus]
	l.cursor = max(min(pos, len(l.text)), 0)
	p.normal = false
	p.pending = ""
	// Search again, as normal mode searched for nothing.
	p.searched = "-"
}

// browseKey handles a key in normal mode while search results are shown,
// moving through them like less. It returns false if key is not one of its
// keys.
func (p *addrPane) browseKey(key string) bool {
	switch key {
	case "j", input.Down, input.CtrlN:
		p.moveSelection(1)
	case "k", input.Up, input.CtrlP:
		p.moveSelection(-1)
	case "G":
		p.moveSelection(len(p.visible))
	case "f":
		p.moveSelection(p.rows)
	case "b":
		p.moveSelection(-p.rows)
	case "d":
		p.moveSelection(max(p.rows/2, 1))
	case "u":
		p.moveSelection(-max(p.rows/2, 1))
	case input.Enter:
		p.lines[p.focus].complete(p.visible[p.selected].Key)
	case input.Esc:
		p.browsing = false
	default:
		return false
	}
	return true
}

// normalKey handles a key in vi's normal mode, returning true if it submits
// the addresses.
func (p *addrPane) normalKey(key string) bool {
	l := &p.lines[p.focus]
	if pending := p.pending; pending != "" {
		p.pending = ""
		p.pendingKey(pending, key)
		return false
	}
	if p.browsing && len(p.visible) > 0 {
		if key == "g" {
			p.pending = key
			return false
		}
		if p.browseKey(key) {
			return false
		}
	}
	if to, ok := l.motion(key); ok {
		l.cursor = to
		return false
	}
	switch key {
	case input.Enter:
		return true
	case input.Backspace, input.CtrlH:
		l.cursor = max(l.cursor-1, 0)
	case "j", input.Down, input.CtrlN:
		p.focus = min(p.focus+1, len(p.lines)-1)
	case "k", input.Up, input.CtrlP:
		p.focus = max(p.focus-1, 0)
	case "G":
		p.focus = len(p.lines) - 1
	case input.Tab:
		p.focus = (p.focus + 1) % len(p.lines)
	case input.BackTab:
		p.focus = (p.focus + len(p.lines) - 1) % len(p.lines)
	case "x", input.Delete:
		l.deleteRange(l.cursor, l.cursor+1)
	case "D":
		l.deleteRange(l.cursor, len(l.text))
	case "C":
		l.deleteRange(l.cursor, len(l.text))
		p.insertMode(l.cursor)
	case "d", "c", "r", "g":
		p.pending = key
	case "i":
		p.insertMode(l.cursor)
	case "a":
		p.insertMode(l.cursor + 1)
	case "I":
		p.insertMode(0)
	case "A":
		p.insertMode(len(l.text))
	case input.CtrlU:
		*l = addrLine{}
	default:
		if pasted, ok := strings.CutPrefix(key, input.PasteStart); ok {
			l.paste(pasted)
		}
	}
	return false
}

// pendingKey handles the key after an operator key in normal mode: the
// motion after d or c, the character after r, or the second g of gg.
func (p *addrPane) pendingKey(op, key string) {
	l := &p.lines[p.focus]
	switch op {
	case "g":
		if key != "g" {
			return
		}
		if p.browsing && len(p.visible) > 0 {
			p.selected = 0
		} else {
			p.focus = 0
		}
	case "r":
		if isPrintable(key) && len([]rune(key)) == 1 &&
			l.cursor < len(l.text) {
			l.set(string(l.text[:l.cursor])+key,
				string(l.text[l.cursor+1:]))
			l.cursor--
		}
	case "d", "c":
		start, end := 0, len(l.text)
		if key != op {
			to, ok := l.motion(key)
			if !ok {
				return
			}
			if op == "c" && (key == "w" || key == "W") {
				to = wordEnd(l.text, l.cursor, key == "W")
			}
			start, end = l.cursor, to
		}
		l.deleteRange(start, end)
		if op == "c" {
			p.insertMode(l.cursor)
		}
	}
}

// Addresses asks for the To, CC and BCC addresses of a message, on three
// lines that Tab moves between. Each searches opts for the address the
// cursor is in, and Down and Up move through what it found. Enter puts the
// selected result in the line, or, with none shown, returns all three lines,
// as addresses joined by ", ". Esc switches to vi's normal mode.
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
