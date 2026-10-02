package display

import (
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

// term is the part of a terminal that Draw's output needs: absolute cursor
// moves, a scroll region, scrolling it up and down, and saving the cursor.
// Text past the right edge overwrites the last column, as with wrap off.
// Colours and modes are ignored. Checked against pyte, a terminal emulator
// that does not share this code, on the cases TestDrawCached generates.
type term struct {
	w, h      int
	rows      [][]rune
	y, x      int
	top, bot  int // Scroll region, 0-based and inclusive.
	savY, svX int
}

func newTerm(w, h int) *term {
	t := &term{w: w, h: h, top: 0, bot: h - 1}
	t.rows = make([][]rune, h)
	for i := range t.rows {
		t.rows[i] = []rune(strings.Repeat(" ", w))
	}
	return t
}

func (t *term) blank() []rune { return []rune(strings.Repeat(" ", t.w)) }

func (t *term) feed(s string) {
	for len(s) > 0 {
		switch {
		case strings.HasPrefix(s, "\033P"):
			// DCS, such as the atomic update markers: skip it.
			end := strings.Index(s, "\033\\")
			s = s[end+2:]
		case strings.HasPrefix(s, "\033["):
			i := 2
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			t.csi(s[2:i], s[i])
			s = s[i+1:]
		default:
			r := []rune(s)[0]
			if t.x >= t.w {
				t.x = t.w - 1
			}
			t.rows[t.y][t.x] = r
			t.x++
			s = s[len(string(r)):]
		}
	}
}

func (t *term) csi(params string, final byte) {
	var n []int
	if !strings.HasPrefix(params, "?") {
		for _, p := range strings.Split(params, ";") {
			v, _ := strconv.Atoi(p)
			n = append(n, v)
		}
	}
	arg := func(i, def int) int {
		if i < len(n) && n[i] > 0 {
			return n[i]
		}
		return def
	}
	switch final {
	case 'H':
		t.y = min(arg(0, 1), t.h) - 1
		t.x = min(arg(1, 1), t.w) - 1
	case 'r':
		top, bot := arg(0, 1)-1, min(arg(1, t.h), t.h)-1
		if top < bot { // A terminal ignores a smaller region.
			t.top, t.bot = top, bot
			t.y, t.x = 0, 0
		}
	case 'S':
		for i := 0; i < arg(0, 1); i++ {
			copy(t.rows[t.top:t.bot], t.rows[t.top+1:t.bot+1])
			t.rows[t.bot] = t.blank()
		}
	case 'T':
		for i := 0; i < arg(0, 1); i++ {
			copy(t.rows[t.top+1:t.bot+1], t.rows[t.top:t.bot])
			t.rows[t.top] = t.blank()
		}
	case 's':
		t.savY, t.svX = t.y, t.x
	case 'u':
		t.y, t.x = t.savY, t.svX
	}
}

func (t *term) lines() []string {
	var ret []string
	for _, r := range t.rows {
		ret = append(ret, strings.TrimRight(string(r), " "))
	}
	return ret
}

// captureDraw runs s.Draw and returns what it wrote.
func captureDraw(t *testing.T, s *Screen) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "draw")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stdout := os.Stdout
	os.Stdout = f
	s.Draw()
	os.Stdout = stdout
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDrawCached draws a screen, then a changed one with UseCache, and
// checks that the terminal ends up showing the second. The second is mostly
// the first shifted, as when scrolling a list, which is what makes Draw
// scroll the terminal rather than redraw. Before the scroll region and the
// record of what it moved were fixed, 56% of these cases came out wrong.
func TestDrawCached(t *testing.T) {
	defer func(v bool) { *useSuspend = v }(*useSuspend)
	*useSuspend = false
	r := rand.New(rand.NewSource(1))
	words := []string{"", "aa", "bb", "cc", "dd", "ee"}
	scrolled := 0
	for i := 0; i < 2000; i++ {
		w, h := 10, 3+r.Intn(10)
		prev := make([]string, h)
		for j := range prev {
			prev[j] = words[r.Intn(len(words))]
		}
		cur := make([]string, h)
		shift := r.Intn(2*h) - h
		for j := range cur {
			if k := j + shift; k >= 0 && k < h && r.Intn(4) > 0 {
				cur[j] = prev[k]
			} else {
				cur[j] = words[r.Intn(len(words))]
			}
		}

		tm := newTerm(w, h)
		s := NewScreen2(w, h)
		for j, l := range prev {
			s.Printlnf(j, "%s", l)
		}
		tm.feed(captureDraw(t, s))
		for j, l := range cur {
			s.Printlnf(j, "%s", l)
		}
		s.UseCache()
		out := captureDraw(t, s)
		if strings.ContainsAny(out, "ST") {
			scrolled++
		}
		tm.feed(out)
		got := tm.lines()
		for j := range cur {
			if got[j] != cur[j] {
				t.Fatalf("case %d: from %q, drawing %q "+
					"shows %q", i, prev, cur, got)
			}
		}
	}
	// Most cases should have taken the scrolling path being tested.
	if scrolled < 1000 {
		t.Errorf("only %d of 2000 cases scrolled", scrolled)
	}
}
