package main

import "testing"

func TestHalfPage(t *testing.T) {
	for _, tc := range []struct{ rows, want int }{
		{40, 20},
		{41, 20},
		{4, 2},
		// Below four rows there is no useful half, so ^D and ^U
		// fall back to moving one row rather than none.
		{3, 1},
		{2, 1},
		{1, 1},
		{0, 1},
		{-5, 1},
	} {
		if got := halfPage(tc.rows); got != tc.want {
			t.Errorf("halfPage(%d) = %d, want %d",
				tc.rows, got, tc.want)
		}
	}
}
