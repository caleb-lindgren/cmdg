package main

// Navigation this fork adds, kept out of upstream's files so that only the
// switch cases that dispatch to it live there. See internal/customize for how
// the keys reach these.

// fullPage is how far f and b move through the message list, of the given
// number of content rows: a screen, and never less than one row.
func fullPage(rows int) int {
	return max(rows, 1)
}

// halfPage is how far ^D and ^U move through the given number of
// content rows: half of what a full page moves, and never less than
// one row, so that a terminal too short to have a useful page size
// still scrolls rather than sitting still.
func halfPage(rows int) int {
	if n := rows / 2; n > 1 {
		return n
	}
	return 1
}
