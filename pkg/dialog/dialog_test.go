package dialog

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ThomasHabets/cmdg/pkg/display"
)

func TestTrimOneChar(t *testing.T) {
	for _, test := range []struct {
		in  string
		out string
	}{
		{"", ""},
		{"a", ""},
		{"ab", "a"},
		{"fö", "f"},
		{"för", "fö"},
		{"ಠ_ಠ", "ಠ_"},
	} {
		if got, want := TrimOneChar(test.in), test.out; got != want {
			t.Errorf("For %q got %q, want %q", test.in, got, want)
		}
	}
}

func TestFilterSubmatch(t *testing.T) {
	a := &Option{Label: "foo"}
	b := &Option{Label: "bar"}

	for _, test := range []struct {
		in     []*Option
		filter string
		out    []*Option
	}{
		{
			in:     []*Option{a, b},
			filter: "",
			out:    []*Option{a, b},
		},
		{
			in:     []*Option{a, b},
			filter: "bice",
			out:    nil,
		},
		{
			in:     []*Option{a, b},
			filter: "fo",
			out:    []*Option{a},
		},
	} {
		if got, want := filterSubmatch(test.in, test.filter), test.out; !reflect.DeepEqual(got, want) {
			t.Errorf("For %q with filter %q got %q, want %q", test.in, test.filter, got, want)
		}
	}
}

// TestDrawOptionsFitsScreen draws more options than fit below the prompt and
// checks that only those that fit are drawn and selectable.
func TestDrawOptionsFitsScreen(t *testing.T) {
	var opts []string
	for i := 0; i < 1000; i++ {
		opts = append(opts, fmt.Sprintf("a%d@example.com", i))
	}
	screen := display.NewScreen2(40, 8)
	got := drawOptions(screen, 3, "", Strings2Options(opts), 0)
	if got != 5 {
		t.Errorf("drew %d options on 5 free rows, want 5", got)
	}
	got = drawOptions(screen, 3, "", Strings2Options(opts[:2]), 0)
	if got != 2 {
		t.Errorf("drew %d of 2 options, want 2", got)
	}
}
