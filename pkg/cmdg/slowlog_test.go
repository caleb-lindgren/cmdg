package cmdg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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

// TestRetryFailed checks that a read whose connection fails is made again
// and answered, and that a request that changes something is not.
func TestRetryFailed(t *testing.T) {
	var m sync.Mutex
	seen := map[string]int{}
	ts := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			m.Lock()
			seen[r.URL.Path]++
			n := seen[r.URL.Path]
			m.Unlock()
			if n == 1 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			_, _ = fmt.Fprintf(w, "answer %d", n)
		}))
	defer ts.Close()
	c := &http.Client{Transport: &slowLogTransport{}}
	resp, err := c.Get(ts.URL + "/read")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "answer 2" {
		t.Errorf("read: got %q, want the second attempt answered", b)
	}
	if _, err := c.Post(ts.URL+"/send", "text/plain",
		strings.NewReader("mail")); err == nil {
		t.Errorf("send: got no error, want the failure")
	}
	m.Lock()
	defer m.Unlock()
	if seen["/send"] != 1 {
		t.Errorf("send made %d times, want once", seen["/send"])
	}
}

// freezeConn passes on what it reads until frozen is set, and from then
// on reads nothing until it is closed, as a connection that died without
// closing does.
type freezeConn struct {
	net.Conn
	frozen *atomic.Bool
	closed chan struct{}
	once   sync.Once
}

func (c *freezeConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if c.frozen.Load() {
		<-c.closed
		return 0, net.ErrClosed
	}
	return n, err
}

func (c *freezeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// TestDeadHTTP2Connection checks that a read stalled on an HTTP/2
// connection that stops answering, as one did in logs/harvard.log, is
// made again on a new connection once a ping goes unanswered, well
// before the header timeout would make it again on the same connection.
func TestDeadHTTP2Connection(t *testing.T) {
	oldTimeouts, oldAfter, oldTimeout := headerTimeouts,
		http2PingAfter, http2PingTimeout
	headerTimeouts = []time.Duration{5 * time.Second, 5 * time.Second}
	http2PingAfter = 50 * time.Millisecond
	http2PingTimeout = 50 * time.Millisecond
	defer func() {
		headerTimeouts, http2PingAfter, http2PingTimeout =
			oldTimeouts, oldAfter, oldTimeout
	}()

	ts := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "%s", r.Proto)
		}))
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()

	var frozen atomic.Bool
	var dials atomic.Int32
	tp := ts.Client().Transport.(*http.Transport).Clone()
	tp.ForceAttemptHTTP2 = true
	tp.DialContext = func(ctx context.Context, network,
		addr string) (net.Conn, error) {
		dials.Add(1)
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		// Only the first connection dies.
		f := &frozen
		if dials.Load() > 1 {
			f = new(atomic.Bool)
		}
		return &freezeConn{Conn: conn, frozen: f,
			closed: make(chan struct{})}, nil
	}
	pingQuiet(tp)
	c := &http.Client{Transport: &slowLogTransport{base: tp}}
	get := func() string {
		t.Helper()
		resp, err := c.Get(ts.URL + "/read")
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

	if got := get(); got != "HTTP/2.0" {
		t.Fatalf("got protocol %q, want HTTP/2.0", got)
	}
	frozen.Store(true)
	st := time.Now()
	if got := get(); got != "HTTP/2.0" {
		t.Errorf("got %q after the connection died", got)
	}
	if d := time.Since(st); d > time.Second {
		t.Errorf("took %v, want the dead connection found by ping", d)
	}
	if n := dials.Load(); n != 2 {
		t.Errorf("dialled %d connections, want 2", n)
	}
}
