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
// silently. ShadowedOpenMessageKeys is the tripwire for that, checked
// by TestCustomKeysDoNotShadowUpstream in cmd/cmdg.
package customize

import (
	"strings"
	"unicode/utf8"

	"github.com/ThomasHabets/cmdg/pkg/input"
)

// A Binding moves one of cmdg's keys to a different key.
type Binding struct {
	// Upstream is the key cmdg's own switch statement handles, and
	// UpstreamName is how the upstream help text spells it.
	Upstream     string
	UpstreamName string

	// Key is what to press instead, and Name is how the rewritten
	// help text should spell it.
	Key  string
	Name string
}

// LeaveHelp is the key that dismisses the help screen. It is used by
// every view, since they share one help() implementation.
var LeaveHelp = Binding{
	Upstream:     input.Enter,
	UpstreamName: "enter",
	Key:          input.Esc,
	Name:         "ESC",
}

// openMessageBindings are the rebindings for the open-message view,
// which move scrolling and paging onto vi-style keys. Anything not
// listed here keeps its upstream key. Listing a key as its own
// replacement, as delete does, changes nothing but marks it as one to
// keep an eye on.
var openMessageBindings = []Binding{
	{Upstream: "u", UpstreamName: "u", Key: input.Esc, Name: "ESC"},
	{Upstream: "n", UpstreamName: "n", Key: "j", Name: "j"},
	{Upstream: "p", UpstreamName: "p", Key: "k", Name: "k"},
	{Upstream: " ", UpstreamName: "space", Key: "f", Name: "f"},
	{
		Upstream: input.Backspace, UpstreamName: "backspace",
		Key: "b", Name: "b",
	},
	{Upstream: "f", UpstreamName: "f", Key: "w", Name: "w"},
	{Upstream: "d", UpstreamName: "d", Key: "d", Name: "d"},
}

// unbound is returned for a key whose upstream meaning has been moved
// elsewhere and that has no local meaning of its own. It matches no
// case in any of cmdg's switches, so it reaches their default branch
// and is logged as an unknown key — which is what pressing a key that
// does nothing should do.
const unbound = "\x00unbound"

var openMessageKeys = dispatch(openMessageBindings)

// dispatch builds one view's pressed-key to upstream-key lookup.
func dispatch(bs []Binding) map[string]string {
	m := make(map[string]string, 2*len(bs))
	// Every rebound key first loses its upstream meaning...
	for _, b := range bs {
		m[b.Upstream] = unbound
	}
	// ...and only then gains its new one. Two passes rather than
	// one, so that a key which is both vacated and reused — "f" is
	// page down here, while forward moves off to "w" — ends up
	// reused rather than unbound, and so that a single lookup can
	// never cascade into a second.
	for _, b := range bs {
		m[b.Key] = b.Upstream
	}
	return m
}

// OpenMessageKey translates a keypress in the open-message view into
// the key cmdg's own switch statement expects. Keys that are not
// customized are returned unchanged.
//
// It must be applied to that switch only, not to the input channel as
// a whole: the same channel carries typing into the compose and search
// dialogs, where a "j" has to stay a "j".
func OpenMessageKey(key string) string {
	if k, ok := openMessageKeys[key]; ok {
		return k
	}
	return key
}

// helpSeparator is what upstream's help lines put between the keys and
// the description of what they do.
const helpSeparator = " — "

// OpenMessageHelp rewrites cmdg's open-message help text: upstream key
// names are replaced by the local ones, and each line is re-laid out
// with the description first.
//
// It reads upstream's text rather than replacing it, so a key added
// upstream shows up in the help without anything here being touched.
// Lines that are not "keys — description" are passed through as they
// are, so an upstream rewording degrades to upstream's own formatting
// rather than to garbage.
func OpenMessageHelp(help string) string {
	return layout(remapNames(help, openMessageBindings))
}

// MessageListHelp rewrites cmdg's message-list help text. Nothing is
// rebound in that view except the key that leaves the help screen, so
// the text keeps upstream's own layout; wrap this in layout() too if
// the two help screens should match.
func MessageListHelp(help string) string {
	return remapNames(help, nil)
}

// remapNames replaces upstream key names with the local ones, in the
// key column of each help line and in the "Press [enter] to exit"
// footer. The key column is padded back to the width upstream gave it,
// so that a caller which does not re-lay-out the text keeps upstream's
// alignment.
func remapNames(help string, bs []Binding) string {
	names := make(map[string]string, len(bs))
	for _, b := range bs {
		names[b.UpstreamName] = b.Name
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
			if n, ok := names[k]; ok {
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
// help text that a local rebinding would make unreachable, leaving out the
// ones it displaces deliberately.
//
// This is the check to run after merging upstream. A local binding is
// translated before cmdg's switch ever sees the key, so an upstream
// binding added on a key this fork already uses would simply never
// fire, with nothing at runtime to say so. It compares help-text key
// names, so it catches only keys upstream spells the way the bindings
// above do — which covers plain letter keys, and those are what get
// added.
func ShadowedOpenMessageKeys(help string) []string {
	displaced := map[string]bool{}
	local := map[string]bool{}
	for _, b := range openMessageBindings {
		displaced[b.UpstreamName] = true
		if b.Key != b.Upstream {
			local[b.Name] = true
		}
	}
	var shadowed []string
	for _, line := range strings.Split(help, "\n") {
		keys, _, ok := strings.Cut(line, helpSeparator)
		if !ok {
			continue
		}
		for _, k := range splitKeys(keys) {
			if local[k] && !displaced[k] {
				shadowed = append(shadowed, k)
			}
		}
	}
	return shadowed
}

// splitKeys splits a help line's key column into its individual keys.
func splitKeys(keys string) []string {
	return strings.Split(strings.TrimRight(keys, " "), ", ")
}
