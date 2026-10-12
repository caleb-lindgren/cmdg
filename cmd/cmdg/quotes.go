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
// attribution was wrapped; or everything from an Outlook "Original
// Message" line to the end.
func quoteBlocks(lines []string) [][2]int {
	var ret [][2]int
	for n := 0; n < len(lines); n++ {
		if outlookRE.MatchString(lines[n]) {
			ret = append(ret, [2]int{n, len(lines)})
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
