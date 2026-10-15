package cmdg

import (
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

// slowRequestAfter is how long a request runs before it is logged as slow,
// and again each time that much longer, so that a request that never
// finishes is in the log, where otherwise only finished ones are.
var slowRequestAfter = 5 * time.Second

// slowLogTransport logs HTTP requests that take long through base, while
// they are still waiting and once they finish.
type slowLogTransport struct {
	base http.RoundTripper
}

// RoundTrip makes the request r through t.base.
func (t *slowLogTransport) RoundTrip(r *http.Request) (*http.Response,
	error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	st := time.Now()
	what := r.Method + " " + r.URL.Host + r.URL.Path
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(slowRequestAfter)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				d := time.Since(st).Round(time.Millisecond)
				log.Warningf("HTTP %s still waiting after %v",
					what, d)
			}
		}
	}()
	resp, err := base.RoundTrip(r)
	if d := time.Since(st); d >= slowRequestAfter {
		status := "error"
		if resp != nil {
			status = resp.Status
		}
		log.Warningf("HTTP %s took %v: %s, %v", what,
			d.Round(time.Millisecond), status, err)
	}
	return resp, err
}
