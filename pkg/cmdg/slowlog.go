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
	// up on and made again. A request stalled on one connection is
	// usually answered at once on another, which the ping that
	// http2PingAfter sets off sees to: a connection that does not
	// answer it is closed, failing its requests, which are then made
	// again on a new one. They are on the response's headers only, so
	// that a large message or attachment can take as long as it takes
	// to download.
	headerTimeouts = []time.Duration{
		10 * time.Second, 20 * time.Second, 40 * time.Second}

	// http2PingAfter is how long an HTTP/2 connection goes without
	// receiving anything before it is pinged, and http2PingTimeout how
	// long it then waits for the answer before it is closed. Together
	// they are under the first of headerTimeouts, so that a request
	// stalled on a dead connection is made again on a new one rather
	// than on the dead one.
	http2PingAfter   = 4 * time.Second
	http2PingTimeout = 4 * time.Second
)

// pingQuiet has tp ping an HTTP/2 connection that has gone quiet, and close
// it if the ping is not answered, so that a connection that died without
// closing fails its requests, which slowLogTransport then makes again on a
// new one, rather than taking every request after them with it. Without
// this, a dead connection stays in the pool until the OS gives up on it,
// which can take many minutes. Added by this fork.
func pingQuiet(tp *http.Transport) {
	tp.HTTP2 = &http.HTTP2Config{
		SendPingTimeout: http2PingAfter,
		PingTimeout:     http2PingTimeout,
	}
}

// slowLogTransport makes requests through base, logs those that take
// long, while they are still waiting and once they finish, and makes again
// those that can be repeated and fail, or get no response within
// headerTimeouts.
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
		if r.Context().Err() != nil {
			return nil, err
		}
		// A request on a connection found dead, by an unanswered
		// ping, fails rather than timing out, and is made again
		// as well.
		if !timedOut {
			log.Warningf("HTTP %s failed: %v, attempt %d of %d",
				what, err, n+1, len(headerTimeouts))
			continue
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
	after := slowRequestAfter
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(after)
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
	if d := time.Since(st); d >= after {
		status := "error"
		if resp != nil {
			status = resp.Status
		}
		log.Warningf("HTTP %s took %v: %s, %v", what,
			d.Round(time.Millisecond), status, err)
	}
	return resp, err
}
