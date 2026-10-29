package main

import (
	"reflect"
	"testing"
)

func TestSplitAddresses(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a@x.com", []string{"a@x.com"}},
		{"a@x.com, b@x.com", []string{"a@x.com,", "b@x.com"}},
		{`"Doe, Jane" <j@x.com>,Bob <b@x.com>`,
			[]string{`"Doe, Jane" <j@x.com>,`, "Bob <b@x.com>"}},
		{`"say \"hi, there\"" <h@x.com>, c@x.com`,
			[]string{`"say \"hi, there\"" <h@x.com>,`, "c@x.com"}},
		{"d@x.com (Doe, D), e@x.com",
			[]string{"d@x.com (Doe, D),", "e@x.com"}},
		{"a@x.com, , b@x.com,", []string{"a@x.com,", "b@x.com,"}},
	} {
		if got := splitAddresses(tc.in); !reflect.DeepEqual(got,
			tc.want) {
			t.Errorf("splitAddresses(%q) = %q, want %q", tc.in,
				got, tc.want)
		}
	}
}

func TestWrapHeader(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		width       int
		want        []string
	}{
		{"CC", "", 20, []string{"CC: "}},
		{"To", "a@x.com, b@x.com", 40,
			[]string{"To: a@x.com, b@x.com"}},
		// Exactly fills the width.
		{"To", "a@x.com, b@x.com", 20,
			[]string{"To: a@x.com, b@x.com"}},
		{"To", "a@x.com, b@x.com", 19,
			[]string{"To: a@x.com,", "    b@x.com"}},
		{"CC", "Ann <a@x.com>, Bob <b@x.com>, Cy <c@x.com>", 32,
			[]string{
				"CC: Ann <a@x.com>,",
				"    Bob <b@x.com>, Cy <c@x.com>",
			}},
		// An address too long for a line of its own is broken.
		{"To", "abcdefghij@x.com", 10,
			[]string{"To: abcdef", "ghij@x.com"}},
		{"To", "a@x.com", 0, []string{"To: a@x.com"}},
	} {
		got := wrapHeader(tc.name, tc.value, tc.width)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("wrapHeader(%q, %q, %d) = %q, want %q",
				tc.name, tc.value, tc.width, got, tc.want)
		}
	}
}
