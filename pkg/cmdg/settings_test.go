package cmdg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeDrive serves the app folder files, two to a page of a listing, and
// counts the listings made.
type fakeDrive struct {
	files map[string]string // name to content, in IDs "id-"+name

	m     sync.Mutex
	lists int
}

func (f *fakeDrive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/drive/v3/files":
		f.m.Lock()
		f.lists++
		f.m.Unlock()
		var names []string
		for n := range f.files {
			names = append(names, n)
		}
		// In a set order, the other files first, to make the
		// listing go on to a second page.
		sort.Slice(names, func(i, j int) bool {
			oi := strings.HasPrefix(names[i], "other")
			oj := strings.HasPrefix(names[j], "other")
			if oi != oj {
				return oi
			}
			return names[i] < names[j]
		})
		start := 0
		if r.URL.Query().Get("pageToken") == "2" {
			start = 2
		}
		type file struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		resp := struct {
			Files         []file `json:"files"`
			NextPageToken string `json:"nextPageToken,omitempty"`
		}{}
		for i := start; i < len(names) && i < start+2; i++ {
			resp.Files = append(resp.Files,
				file{"id-" + names[i], names[i]})
		}
		if start == 0 && len(names) > 2 {
			resp.NextPageToken = "2"
		}
		_ = json.NewEncoder(w).Encode(resp)
	case strings.HasPrefix(r.URL.Path, "/drive/v3/files/id-"):
		name := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/id-")
		_, _ = w.Write([]byte(f.files[name]))
	default:
		http.NotFound(w, r)
	}
}

// TestLoadSettings checks that the settings and another file are loaded in
// one listing of the folder, across its pages, and that a folder without
// them is not an error.
func TestLoadSettings(t *testing.T) {
	for _, tc := range []struct {
		name       string
		files      map[string]string
		wantSig    string
		wantSender string
		wantLists  int
	}{
		{
			name: "both, on the second page",
			files: map[string]string{
				"other1":        "x",
				"other2":        "y",
				"signature.txt": "-- \nMe",
				settingsFileName: `{"sender":` +
					`"Me <me@example.com>"}`,
			},
			wantSig:    "-- \nMe",
			wantSender: "Me <me@example.com>",
			wantLists:  2,
		},
		{
			name:      "neither",
			files:     map[string]string{"other1": "x"},
			wantLists: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDrive{files: tc.files}
			ts := httptest.NewServer(f)
			defer ts.Close()
			conn, err := NewFake(&http.Client{
				Transport: &redirector{ts.URL}})
			if err != nil {
				t.Fatal(err)
			}
			m, err := conn.LoadSettings(context.Background(),
				"signature.txt")
			if err != nil {
				t.Fatal(err)
			}
			if got := string(m["signature.txt"]); got != tc.wantSig {
				t.Errorf("got signature %q, want %q", got,
					tc.wantSig)
			}
			if _, ok := m[settingsFileName]; ok {
				t.Errorf("settings file returned as well")
			}
			if got := conn.GetDefaultSender(); got != tc.wantSender {
				t.Errorf("got sender %q, want %q", got,
					tc.wantSender)
			}
			if f.lists != tc.wantLists {
				t.Errorf("listed the folder %d times, want %d",
					f.lists, tc.wantLists)
			}
		})
	}
}
