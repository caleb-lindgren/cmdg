package main

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// splitAddresses splits an address list header, such as To, into its
// addresses, each but the last keeping the comma after it. A comma inside a
// quoted name, an angle-bracketed address or a comment does not separate
// addresses.
func splitAddresses(s string) []string {
	var ret []string
	inQuote, escaped := false, false
	angle, paren := 0, 0
	start := 0
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case inQuote:
		case r == '<':
			angle++
		case r == '>' && angle > 0:
			angle--
		case r == '(':
			paren++
		case r == ')' && paren > 0:
			paren--
		case r == ',' && angle == 0 && paren == 0:
			if a := strings.TrimSpace(s[start : i+1]); a != "," {
				ret = append(ret, a)
			}
			start = i + 1
		}
	}
	if a := strings.TrimSpace(s[start:]); a != "" {
		ret = append(ret, a)
	}
	return ret
}

// wrapHeader returns the header line "name: value" broken into lines at
// most width columns wide. An address list is broken between addresses,
// with the lines after the first indented to line up with the first
// address. Only an address too long for a line of its own is broken
// inside.
func wrapHeader(name, value string, width int) []string {
	prefix := name + ": "
	if width < 1 {
		return []string{prefix + value}
	}
	indent := strings.Repeat(" ", runewidth.StringWidth(prefix))
	var ret []string
	cur, n := prefix, 0 // n is how many addresses cur has.
	for _, a := range splitAddresses(value) {
		next := cur + a
		if n > 0 {
			next = cur + " " + a
			if runewidth.StringWidth(next) > width {
				ret = append(ret, wrapLine(cur, width)...)
				next, n = indent+a, 0
			}
		}
		cur = next
		n++
	}
	return append(ret, wrapLine(cur, width)...)
}
