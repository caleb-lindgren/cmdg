// Package customize holds this fork's local keyboard customizations.
//
// Upstream cmdg hardcodes its key bindings in the switch statement of
// each view, and spells them out in a help string next to it. Editing
// those in place turns every upstream change to the same lines into a
// merge conflict, so this package inverts the arrangement: upstream's
// switch statements and help strings stay byte-identical to upstream,
// and each view calls in here instead — once to translate a keypress
// before the switch sees it, once to rewrite the help text before it
// is displayed. Rebinding a key is then a change to this file only.
//
// The one thing that buys has to be paid for: because a local binding
// is applied before the switch, it shadows any upstream binding on the
// same key, and an upstream merge can introduce such a collision
// silently. ShadowedOpenMessageKeys and ShadowedMessageListKeys are
// the tripwire for that, checked by TestCustomKeysDoNotShadowUpstream
// in cmd/cmdg.
//
// A binding that upstream has no equivalent for at all cannot be
// translated into anything, so it gets a pseudo-key here and one added
// case in the view's own switch. GoBottom is the only one so far.
package customize

import (
	"strings"
	"unicode/utf8"

	"github.com/ThomasHabets/cmdg/pkg/input"
)

// A Binding is one local key binding: a sequence of keypresses
// standing in for a key that cmdg's own switch statement handles.
type Binding struct {
	// Keys is the sequence to press, one element per keypress. Two
	// elements make it a chord, as "g" then "g" is.
	Keys []string
	// Name is how the help text should spell Keys.
	Name string

	// Upstream is the key cmdg's switch handles once Keys has been
	// translated, and UpstreamName is how upstream's own help text
	// spells it. An empty UpstreamName means upstream's help does
	// not mention that key, so Help has to supply a line for it.
	Upstream     string
	UpstreamName string

	// Help describes the binding on the help line added for it.
	// Leave it empty when upstream's help already carries a line,
	// which UpstreamName is then used to find and rewrite.
	Help string

	// Alias makes Keys an additional way to reach Upstream. By
	// default Keys replaces it, and pressing Upstream itself stops
	// doing anything at all.
	Alias bool
}

// GoBottom, HalfPageDown and HalfPageUp are delivered to a view's
// switch for keys that upstream has no binding to translate into, so
// unlike every other binding here they reach cases the fork adds
// itself. Each view carries one case per constant.
const (
	GoBottom     = "\x00go-bottom"
	HalfPageDown = "\x00half-page-down"
	HalfPageUp   = "\x00half-page-up"
)

// ctrlD is ^D. pkg/input names most control keys but not this one, and
// naming it there would put the fork into a third upstream file for
// one line.
const ctrlD = "\x04"

// unbound is delivered for a key whose upstream meaning has been moved
// elsewhere and that has no local meaning of its own, and for the key
// that breaks off a half-typed chord. It matches no case in any of
// cmdg's switches, so it reaches their default branch and is logged as
// an unknown key — which is what pressing a key that does nothing
// should do.
const unbound = "\x00unbound"

// openMessageBindings are the rebindings for the open-message view,
// which move scrolling and paging onto vi-style keys. Anything not
// listed here keeps its upstream key. Listing a key as its own
// replacement, as delete does, changes nothing but marks it as one to
// keep an eye on.
var openMessageBindings = []Binding{
	{
		Keys: []string{input.Esc}, Name: "ESC",
		Upstream: "u", UpstreamName: "u",
	},
	{
		Keys: []string{"j"}, Name: "j",
		Upstream: "n", UpstreamName: "n",
	},
	{
		Keys: []string{"k"}, Name: "k",
		Upstream: "p", UpstreamName: "p",
	},
	{
		Keys: []string{"f"}, Name: "f",
		Upstream: " ", UpstreamName: "space",
	},
	{
		Keys: []string{"b"}, Name: "b",
		Upstream: input.Backspace, UpstreamName: "backspace",
	},
	{
		Keys: []string{"w"}, Name: "w",
		Upstream: "f", UpstreamName: "f",
	},
	{
		Keys: []string{"d"}, Name: "d",
		Upstream: "d", UpstreamName: "d",
	},
	{
		Keys: []string{"/"}, Name: "/",
		Upstream: "s", UpstreamName: "s",
	},
	{
		Keys: []string{ctrlD}, Name: "^D",
		Upstream: HalfPageDown, Help: "Half page down", Alias: true,
	},
	{
		Keys: []string{input.CtrlU}, Name: "^U",
		Upstream: HalfPageUp, Help: "Half page up", Alias: true,
	},
	{
		// Upstream binds Home to this but does not document it,
		// so the added line is the only one the help gets.
		Keys: []string{"g", "g"}, Name: "gg",
		Upstream: input.Home, Help: "Go to top", Alias: true,
	},
	{
		Keys: []string{"G"}, Name: "G",
		Upstream: GoBottom, Help: "Go to bottom", Alias: true,
	},
}

// messageListBindings are the rebindings for the message list. Its
// scrolling keys are vi-style upstream already, so only search and the
// two ends of the list need moving — and "g" has to give up being "go
// to label" on its own to become the prefix that "gg" needs.
var messageListBindings = []Binding{
	{
		Keys: []string{"/"}, Name: "/",
		Upstream: "s", UpstreamName: "s",
	},
	{
		Keys: []string{"g", "l"}, Name: "gl",
		Upstream: "g", UpstreamName: "g",
	},
	{
		Keys: []string{ctrlD}, Name: "^D",
		Upstream: HalfPageDown, Help: "Half page down", Alias: true,
	},
	{
		Keys: []string{input.CtrlU}, Name: "^U",
		Upstream: HalfPageUp, Help: "Half page up", Alias: true,
	},
	{
		Keys: []string{"g", "g"}, Name: "gg",
		Upstream: input.Home, Help: "Go to top", Alias: true,
	},
	{
		Keys: []string{"G"}, Name: "G",
		Upstream: GoBottom, Help: "Go to bottom (of what is loaded)",
		Alias: true,
	},
}

// LeaveHelp is the key that dismisses the help screen. It is used by
// every view, since they share one help() implementation.
var LeaveHelp = Binding{
	Keys: []string{input.Esc}, Name: "ESC",
	Upstream: input.Enter, UpstreamName: "enter",
}

// A translator turns a keypress into the key one view's switch wants.
type translator struct {
	// direct holds the bindings reached by a single keypress.
	direct map[string]string
	// chords holds them by first keypress, then second.
	chords map[string]map[string]string
	// pending is the first key of a chord waiting for its second.
	pending string
}

func newTranslator(bs []Binding) *translator {
	t := &translator{
		direct: map[string]string{},
		chords: map[string]map[string]string{},
	}
	// Every replaced key first loses its upstream meaning...
	for _, b := range bs {
		if !b.Alias {
			t.direct[b.Upstream] = unbound
		}
	}
	// ...and only then does a local sequence gain it. Two passes
	// rather than one, so that a key which is both vacated and
	// reused — "f" is page down here, while forward moves off to
	// "w" — ends up reused rather than unbound, and so that a
	// single lookup can never cascade into a second.
	for _, b := range bs {
		switch len(b.Keys) {
		case 1:
			t.direct[b.Keys[0]] = b.Upstream
		case 2:
			p, s := b.Keys[0], b.Keys[1]
			if t.chords[p] == nil {
				t.chords[p] = map[string]string{}
			}
			t.chords[p][s] = b.Upstream
		default:
			panic("customize: Keys must hold one or two keys")
		}
	}
	return t
}

func (t *translator) translate(key string) string {
	if t.pending != "" {
		p := t.pending
		t.pending = ""
		if u, ok := t.chords[p][key]; ok {
			return u
		}
		// The key that breaks off a chord is swallowed with it,
		// the way vi does it: "gx" does nothing at all, rather
		// than doing whatever "x" on its own would have done.
		return unbound
	}
	if _, ok := t.chords[key]; ok {
		t.pending = key
		return unbound
	}
	if u, ok := t.direct[key]; ok {
		return u
	}
	return key
}

var (
	openMessage = newTranslator(openMessageBindings)
	messageList = newTranslator(messageListBindings)
)

// OpenMessageKey translates a keypress in the open-message view into
// the key cmdg's own switch statement expects. Keys that are not
// customized are returned unchanged.
//
// It must be applied to that switch only, not to the input channel as
// a whole: the same channel carries typing into the compose and search
// dialogs, where a "j" has to stay a "j".
//
// Neither this nor MessageListKey is safe for concurrent use, and
// neither needs to be. cmdg reads keys in one goroutine, and only one
// view reads them at a time, since opening a message blocks the
// message list's own loop.
func OpenMessageKey(key string) string {
	return openMessage.translate(key)
}

// MessageListKey is OpenMessageKey for the message list.
func MessageListKey(key string) string {
	return messageList.translate(key)
}

// helpSeparator is what upstream's help lines put between the keys and
// the description of what they do.
const helpSeparator = " — "

// OpenMessageHelp rewrites cmdg's open-message help text: upstream key
// names are replaced by the local ones, lines are added for bindings
// upstream has none for, and the result is re-laid-out with the
// description first.
//
// It reads upstream's text rather than replacing it, so a key added
// upstream shows up in the help without anything here being touched.
// Lines that are not "keys — description" are passed through as they
// are, so an upstream rewording degrades to upstream's own formatting
// rather than to garbage.
func OpenMessageHelp(help string) string {
	return layout(addLines(remapNames(help, openMessageBindings),
		openMessageBindings))
}

// MessageListHelp is OpenMessageHelp for the message list, except that
// it keeps upstream's own layout: that view's help is a much longer
// list, and upstream's key-first column reads better at that length.
// Wrap this in layout() if the two help screens should match.
func MessageListHelp(help string) string {
	return addLines(remapNames(help, messageListBindings),
		messageListBindings)
}

// remapNames replaces upstream key names with the local ones, in the
// key column of each help line and in the "Press [enter] to exit"
// footer. The key column is padded back to the width upstream gave it,
// so that a caller which does not re-lay-out the text keeps upstream's
// alignment.
func remapNames(help string, bs []Binding) string {
	names := make(map[string]string, len(bs))
	for _, b := range bs {
		if b.UpstreamName != "" {
			names[b.UpstreamName] = b.Name
		}
	}
	lines := strings.Split(help, "\n")
	for i, line := range lines {
		keys, desc, ok := strings.Cut(line, helpSeparator)
		if !ok {
			lines[i] = strings.ReplaceAll(line,
				"["+LeaveHelp.UpstreamName+"]",
				"["+LeaveHelp.Name+"]")
			continue
		}
		var out []string
		for _, k := range splitKeys(keys) {
			if n, found := names[k]; found {
				k = n
			}
			out = append(out, k)
		}
		// Cut leaves upstream's padding on the key column, so
		// restoring that width restores its alignment exactly.
		joined := strings.Join(out, ", ")
		pad := utf8.RuneCountInString(keys) -
			utf8.RuneCountInString(joined)
		if pad > 0 {
			joined += strings.Repeat(" ", pad)
		}
		lines[i] = joined + helpSeparator + desc
	}
	return strings.Join(lines, "\n")
}

// addLines adds a help line for each binding upstream's own help does
// not mention, after the last line that it does. The key column is
// padded to the width the surrounding lines use, so the block still
// lines up for a caller that does not re-lay-out it.
func addLines(help string, bs []Binding) string {
	var extra []Binding
	for _, b := range bs {
		if b.Help != "" {
			extra = append(extra, b)
		}
	}
	if len(extra) == 0 {
		return help
	}
	lines := strings.Split(help, "\n")
	last, width := -1, 0
	for i, line := range lines {
		keys, _, ok := strings.Cut(line, helpSeparator)
		if !ok {
			continue
		}
		last = i
		if n := utf8.RuneCountInString(keys); n > width {
			width = n
		}
	}
	if last < 0 {
		return help
	}
	out := make([]string, 0, len(lines)+len(extra))
	out = append(out, lines[:last+1]...)
	for _, b := range extra {
		keys := b.Name
		if pad := width - utf8.RuneCountInString(keys); pad > 0 {
			keys += strings.Repeat(" ", pad)
		}
		out = append(out, keys+helpSeparator+b.Help)
	}
	out = append(out, lines[last+1:]...)
	return strings.Join(out, "\n")
}

// layout re-lays out "keys — description" lines as "description: keys"
// with the keys in an aligned column.
func layout(help string) string {
	lines := strings.Split(help, "\n")
	width := 0
	for _, line := range lines {
		_, desc, ok := strings.Cut(line, helpSeparator)
		if !ok {
			continue
		}
		if n := utf8.RuneCountInString(desc) + len(":"); n > width {
			width = n
		}
	}
	for i, line := range lines {
		keys, desc, ok := strings.Cut(line, helpSeparator)
		if !ok {
			continue
		}
		label := desc + ":"
		pad := width - utf8.RuneCountInString(label) + 1
		lines[i] = label + strings.Repeat(" ", pad) +
			strings.TrimRight(keys, " ")
	}
	return strings.Join(lines, "\n")
}

// ShadowedOpenMessageKeys returns the key names in upstream's own open-message
// help text that a local binding would make unreachable, leaving out the ones
// it displaces deliberately.
//
// This is the check to run after merging upstream. A local binding is
// translated before cmdg's switch ever sees the key, so an upstream
// binding added on a key this fork already claims would never fire,
// with nothing at runtime to say so. It compares help-text key names,
// so it catches only keys upstream spells the way the bindings above
// do — which covers plain letter keys, and those are what get added.
func ShadowedOpenMessageKeys(help string) []string {
	return shadowed(help, openMessageBindings)
}

// ShadowedMessageListKeys is ShadowedOpenMessageKeys for the list.
func ShadowedMessageListKeys(help string) []string {
	return shadowed(help, messageListBindings)
}

func shadowed(help string, bs []Binding) []string {
	displaced := map[string]bool{}
	claimed := map[string]bool{}
	for _, b := range bs {
		if b.UpstreamName != "" {
			displaced[b.UpstreamName] = true
		}
		if n := claimedName(b); n != "" {
			claimed[n] = true
		}
	}
	var out []string
	for _, line := range strings.Split(help, "\n") {
		keys, _, ok := strings.Cut(line, helpSeparator)
		if !ok {
			continue
		}
		for _, k := range splitKeys(keys) {
			if k != "" && claimed[k] && !displaced[k] {
				out = append(out, k)
			}
		}
	}
	return out
}

// claimedName is the help-text spelling of the keypress a binding
// takes over, which is what makes it comparable with the key names in
// upstream's help. A chord claims only its first keypress — that is
// the one that stops working on its own — so "gg" claims "g".
func claimedName(b Binding) string {
	if len(b.Keys) == 1 {
		return b.Name
	}
	if r := []rune(b.Name); len(r) > 0 {
		return string(r[0])
	}
	return ""
}

// splitKeys splits a help line's key column into its individual keys.
func splitKeys(keys string) []string {
	return strings.Split(strings.TrimRight(keys, " "), ", ")
}
