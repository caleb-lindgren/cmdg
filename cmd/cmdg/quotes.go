package main

// Quote folding for conversation mode, added by this fork. A reply
// usually carries the message it replies to, which the conversation view
// already shows above it, so quoted text is folded away into one line,
// and shown again with a key.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ThomasHabets/cmdg/pkg/display"
)

var (
	// attributionRE matches the line introducing a quote, such as
	// "On Mon, 5 Oct 2026 at 10:00, Alice <alice@example.com> wrote:",
	// whose end Gmail may have wrapped onto a line of its own.
	attributionRE = regexp.MustCompile(`(?i)\b(wrote|writes)\s*:\s*$`)

	// outlookRE matches the line Outlook starts the message replied
	// to with, which it does not quote with "> ", so that everything
	// from there to the end is the earlier message.
	outlookRE = regexp.MustCompile(
		`(?i)^\s*-{2,}\s*original message\s*-{2,}\s*$`)

	// headerFromRE, headerSentRE and headerToRE match the lines of the
	// header block Outlook, and other mail programs, start the message
	// replied to with when they do not write "Original Message": From,
	// then Sent or Date, then To or Subject. HTML rendered to text may
	// have the names in bold, as "*From:*".
	headerFromRE = regexp.MustCompile(`(?i)^\s*\*?from:\*?\s+\S`)
	headerSentRE = regexp.MustCompile(`(?i)^\s*\*?(sent|date):\*?\s+\S`)
	headerToRE   = regexp.MustCompile(`(?i)^\s*\*?(to|subject):\*?\s`)

	// ruleRE matches a line drawn above such a header block, as
	// Outlook does with underscores.
	ruleRE = regexp.MustCompile(`^\s*[_─—-]{10,}\s*$`)

	// forwardRE matches the line above the header block of a message
	// forwarded, which is not folded, as it is not quoted.
	forwardRE = regexp.MustCompile(`(?i)forwarded message|begin forwarded`)
)

// isQuoted returns whether line is quoted with "> ".
func isQuoted(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), ">")
}

// isBlank returns whether line has nothing but white space.
func isBlank(line string) bool {
	return strings.TrimSpace(line) == ""
}

// quoteBlocks returns the quoted parts of lines, the lines of a message
// body, as [start, end) line ranges, in order. A part is a run of lines
// quoted with "> ", blank lines within it included, together with the
// attribution line introducing it, and the line before that if the
// attribution was wrapped; or everything from the start of the message
// replied to to the end, as quotedRest finds it.
func quoteBlocks(lines []string) [][2]int {
	var ret [][2]int
	for n := 0; n < len(lines); n++ {
		if start, ok := quotedRest(lines, n); ok {
			ret = append(ret, [2]int{start, len(lines)})
			break
		}
		if !isQuoted(lines[n]) {
			continue
		}
		start := n
		end := n + 1
		for m := n + 1; m < len(lines); m++ {
			if isQuoted(lines[m]) {
				end = m + 1
			} else if !isBlank(lines[m]) {
				break
			}
		}
		start = attributionStart(lines, start)
		ret = append(ret, [2]int{start, end})
		n = end - 1
	}
	return ret
}

// quotedRest returns whether the rest of lines, the lines of a message
// body, from line n, is the message replied to, and the line it starts
// at: at an Outlook "Original Message" line, a header block of From,
// Sent and To, with the rule above it, if any, or an "On <date>, <name>
// wrote:" attribution followed by lines not quoted with "> ", as Apple
// Mail's HTML quote renders. It is not if nothing comes before it, as
// then the message is forwarded rather than replied to, nor if the rule
// says it is forwarded.
func quotedRest(lines []string, n int) (int, bool) {
	start := n
	if a, ok := unquotedAttributionAt(lines, n); ok {
		start = a
	} else if !outlookRE.MatchString(lines[n]) &&
		!headerBlockAt(lines, n) {
		return 0, false
	}
	n = start
	above := n - 1
	for above >= 0 && isBlank(lines[above]) {
		above--
	}
	if above >= 0 && forwardRE.MatchString(lines[above]) {
		return 0, false
	}
	if above >= 0 && ruleRE.MatchString(lines[above]) {
		start = above
		above--
		for above >= 0 && isBlank(lines[above]) {
			above--
		}
	}
	return start, above >= 0
}

// headerBlockAt returns whether line n of lines starts a header block:
// From, then Sent or Date on one of the next two lines, then To or
// Subject within the five after that.
func headerBlockAt(lines []string, n int) bool {
	if !headerFromRE.MatchString(lines[n]) {
		return false
	}
	for s := n + 1; s <= n+2 && s < len(lines); s++ {
		if !headerSentRE.MatchString(lines[s]) {
			continue
		}
		for t := s + 1; t <= s+5 && t < len(lines); t++ {
			if headerToRE.MatchString(lines[t]) {
				return true
			}
		}
	}
	return false
}

// unquotedAttributionAt returns whether line n of lines is the end of an
// attribution starting "On " whose next non-blank line is not quoted with
// "> ", and the line the attribution starts at, the one before if it was
// wrapped.
func unquotedAttributionAt(lines []string, n int) (int, bool) {
	if !attributionRE.MatchString(lines[n]) {
		return 0, false
	}
	next := n + 1
	for next < len(lines) && isBlank(lines[next]) {
		next++
	}
	if next == len(lines) || isQuoted(lines[next]) {
		return 0, false
	}
	start := attributionStart(lines, n+1)
	if !strings.HasPrefix(strings.TrimSpace(lines[start]), "On ") {
		return 0, false
	}
	return start, true
}

// attributionStart returns where the quote starting at line start of lines
// begins once the attribution line before it, if any, is included.
func attributionStart(lines []string, start int) int {
	a := start - 1
	for a >= 0 && isBlank(lines[a]) {
		a--
	}
	if a < 0 || !attributionRE.MatchString(lines[a]) {
		return start
	}
	// "On <date>, <name> <\naddress> wrote:" wrapped.
	if a > 0 && !isBlank(lines[a-1]) &&
		!strings.HasPrefix(strings.TrimSpace(lines[a]), "On ") &&
		strings.HasPrefix(strings.TrimSpace(lines[a-1]), "On ") {
		return a - 1
	}
	return a
}

// foldQuotes returns lines with each quoted part replaced by one line
// saying how many lines were folded away.
func foldQuotes(lines []string) []string {
	var ret []string
	prev := 0
	for _, b := range quoteBlocks(lines) {
		ret = append(ret, lines[prev:b[0]]...)
		ret = append(ret, foldLine(b[1]-b[0]))
		prev = b[1]
	}
	return append(ret, lines[prev:]...)
}

// foldLine is the line that stands in for n folded lines.
func foldLine(n int) string {
	s := "s"
	if n == 1 {
		s = ""
	}
	return fmt.Sprintf("%s[… %d quoted line%s, z to show]%s",
		display.Grey, n, s, display.Reset)
}
