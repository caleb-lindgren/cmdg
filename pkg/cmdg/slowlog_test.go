package cmdg

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

// TestSlowLogTransport checks that a request is logged while it is still
// waiting, and once it finishes, and that a quick one is not.
func TestSlowLogTransport(t *testing.T) {
	old := slowRequestAfter
	slowRequestAfter = 20 * time.Millisecond
	defer func() { slowRequestAfter = old }()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(log.StandardLogger().Out)

	ts := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/slow" {
				time.Sleep(70 * time.Millisecond)
			}
		}))
	defer ts.Close()
	c := &http.Client{Transport: &slowLogTransport{}}
	for _, p := range []string{"/quick", "/slow"} {
		resp, err := c.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	got := buf.String()
	if strings.Contains(got, "/quick") {
		t.Errorf("quick request logged:\n%s", got)
	}
	for _, want := range []string{"/slow still waiting after",
		"/slow took"} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
}

// stallFirst answers each path, but leaves the first request for each
// unanswered until it is given up on, as a stalled connection does, and
// records the bodies of the requests made.
type stallFirst struct {
	m      sync.Mutex
	seen   map[string]int
	bodies []string
}

func (s *stallFirst) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.m.Lock()
	s.seen[r.URL.Path]++
	n := s.seen[r.URL.Path]
	s.bodies = append(s.bodies, string(b))
	s.m.Unlock()
	if n == 1 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	_, _ = fmt.Fprintf(w, "answer %d", n)
}

// TestRetryStalled checks that a stalled read and a stalled token request
// are made again and answered, with the token request's body sent again,
// and that a stalled request that changes something is waited on rather
// than made again.
func TestRetryStalled(t *testing.T) {
	old := headerTimeouts
	headerTimeouts = []time.Duration{30 * time.Millisecond,
		30 * time.Millisecond}
	defer func() { headerTimeouts = old }()

	s := &stallFirst{seen: map[string]int{}}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c := &http.Client{Transport: &slowLogTransport{}}
	body := func(resp *http.Response, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	if got := body(c.Get(ts.URL + "/read")); got != "answer 2" {
		t.Errorf("read: got %q, want the second attempt answered",
			got)
	}
	got := body(c.Post(ts.URL+"/o/oauth2/token",
		"application/x-www-form-urlencoded",
		strings.NewReader("grant_type=refresh_token")))
	if got != "answer 2" {
		t.Errorf("token: got %q, want the second attempt answered",
			got)
	}
	if got := body(c.Post(ts.URL+"/send", "text/plain",
		strings.NewReader("mail"))); got != "answer 1" {
		t.Errorf("send: got %q, want the only attempt answered", got)
	}
	want := []string{"", "", "grant_type=refresh_token",
		"grant_type=refresh_token", "mail"}
	if !reflect.DeepEqual(s.bodies, want) {
		t.Errorf("got request bodies %q, want %q", s.bodies, want)
	}
}

// TestRetryGivesUp checks that a request never answered fails once every
// attempt has timed out.
func TestRetryGivesUp(t *testing.T) {
	old := headerTimeouts
	headerTimeouts = []time.Duration{20 * time.Millisecond,
		20 * time.Millisecond}
	defer func() { headerTimeouts = old }()
	ts := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
	defer ts.Close()
	c := &http.Client{Transport: &slowLogTransport{}}
	_, err := c.Get(ts.URL + "/never")
	if err == nil || !strings.Contains(err.Error(), "no response to") {
		t.Errorf("got %v, want no response", err)
	}
}
