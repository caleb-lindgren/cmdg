package cmdg

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
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
