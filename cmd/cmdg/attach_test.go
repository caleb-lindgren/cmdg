package main

import (
	"os"
	"path"
	"testing"
)

func TestAttachStartDir(t *testing.T) {
	dir := t.TempDir()
	sub := path.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		atts []*file
		want string
	}{
		{"none", nil, "."},
		{"forwarded only", []*file{{name: "x"}}, "."},
		{"most recent local", []*file{
			{path: path.Join(dir, "a")},
			{path: path.Join(sub, "b")},
			{name: "forwarded"},
		}, sub},
		{"dir gone", []*file{{path: path.Join(dir, "gone", "c")}}, "."},
	} {
		if got := attachStartDir(tc.atts); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
