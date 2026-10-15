package cmdg

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	// slowRequestAfter is how long a request runs before it is logged
	// as slow, and again each time that much longer, so that a request
	// that never finishes is in the log, where otherwise only finished
	// ones are.
	slowRequestAfter = 5 * time.Second

	// headerTimeouts are how long each attempt at a request that can
	// be repeated waits for the response to start before it is given
	// up on and made again, on a connection of its own: a request
	// stalled on one connection is usually answered at once on
	// another. They are on the response's headers only, so that a
	// large message or attachment can take as long as it takes to
	// download.
	headerTimeouts = []time.Duration{
		10 * time.Second, 20 * time.Second, 40 * time.Second}
)

// slowLogTransport makes requests through base, logs those that take
// long, while they are still waiting and once they finish, and makes again
// those that can be repeated and get no response within headerTimeouts.
type slowLogTransport struct {
	base http.RoundTripper
}

// repeatable returns whether r can be made again without effect beyond
// its result: a read, or a request for an OAuth token. Anything else, such
// as sending a message, may have been done although no response came, so
// is never repeated, and is waited on as long as it takes.
func repeatable(r *http.Request) bool {
	token := r.URL.Path == "/o/oauth2/token" ||
		(r.URL.Host == "oauth2.googleapis.com" &&
			r.URL.Path == "/token")
	switch {
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
	case r.Method == http.MethodPost && token:
	default:
		return false
	}
	return r.Body == nil || r.Body == http.NoBody || r.GetBody != nil
}

// cancelBody cancels the context of its request once it is closed.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close closes the body and cancels the request.
func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// RoundTrip makes the request r, again if need be.
func (t *slowLogTransport) RoundTrip(r *http.Request) (*http.Response,
	error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	what := r.Method + " " + r.URL.Host + r.URL.Path
	if !repeatable(r) {
		return logSlow(base, r, what)
	}
	var err error
	for n, timeout := range headerTimeouts {
		req := r
		if n > 0 {
			req = r.Clone(r.Context())
			if r.GetBody != nil {
				if req.Body, err = r.GetBody(); err != nil {
					return nil, err
				}
			}
		}
		ctx, cancel := context.WithCancel(req.Context())
		timer := time.AfterFunc(timeout, cancel)
		var resp *http.Response
		resp, err = logSlow(base, req.WithContext(ctx), what)
		timedOut := !timer.Stop()
		if err == nil {
			resp.Body = &cancelBody{resp.Body, cancel}
			return resp, nil
		}
		cancel()
		if !timedOut || r.Context().Err() != nil {
			return nil, err
		}
		err = fmt.Errorf("no response to %s in %v", what, timeout)
		log.Warningf("HTTP %s: no response in %v, attempt %d of %d",
			what, timeout, n+1, len(headerTimeouts))
	}
	return nil, err
}

// logSlow makes the request r through base, logging it if it takes long.
func logSlow(base http.RoundTripper, r *http.Request,
	what string) (*http.Response, error) {
	st := time.Now()
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
