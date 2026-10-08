package cmdg

import (
	"net/http"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// countingSource gives out tokens named by its next field, and counts
// how many it has given out.
type countingSource struct {
	next  string
	calls int
}

func (s *countingSource) Token() (*oauth2.Token, error) {
	s.calls++
	return &oauth2.Token{
		AccessToken: s.next,
		Expiry:      time.Now().Add(time.Hour),
	}, nil
}

// TestAccessToken checks that AccessToken gives the last valid token
// without fetching one, and that a token given to SetAccessToken is used
// until it expires.
func TestAccessToken(t *testing.T) {
	refresh := &countingSource{next: "fetched"}
	// As from a config with only a refresh token.
	c := &CmdG{tokens: newTokenSource(&oauth2.Token{}, refresh)}

	if tok, _ := c.AccessToken(); tok != "" {
		t.Errorf("before any RPC: got %q, want none", tok)
	}
	if refresh.calls != 0 {
		t.Errorf("AccessToken fetched %d tokens", refresh.calls)
	}

	if _, err := c.tokens.Token(); err != nil {
		t.Fatal(err)
	}
	if tok, _ := c.AccessToken(); tok != "fetched" {
		t.Errorf("after an RPC: got %q, want %q", tok, "fetched")
	}

	expiry := time.Now().Add(30 * time.Minute)
	c.SetAccessToken("passed", expiry)
	got, err := c.tokens.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "passed" || refresh.calls != 1 {
		t.Errorf("after SetAccessToken: got %q with %d fetches, "+
			"want %q with 1", got.AccessToken, refresh.calls,
			"passed")
	}
	if tok, exp := c.AccessToken(); tok != "passed" ||
		!exp.Equal(expiry) {
		t.Errorf("after SetAccessToken: got %q expiring %v, "+
			"want %q expiring %v", tok, exp, "passed", expiry)
	}

	refresh.next = "refetched"
	c.SetAccessToken("expired", time.Now().Add(-time.Minute))
	if tok, _ := c.AccessToken(); tok != "" {
		t.Errorf("with an expired token: got %q, want none", tok)
	}
	got, err = c.tokens.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "refetched" || refresh.calls != 2 {
		t.Errorf("after an expired token: got %q with %d fetches, "+
			"want %q with 2", got.AccessToken, refresh.calls,
			"refetched")
	}
}

// TestAccessTokenFake checks that a fake connection, which has no token
// source, has no access token and ignores one set.
func TestAccessTokenFake(t *testing.T) {
	c, err := NewFake(&http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	c.SetAccessToken("passed", time.Now().Add(time.Hour))
	if tok, _ := c.AccessToken(); tok != "" {
		t.Errorf("got %q, want none", tok)
	}
}
