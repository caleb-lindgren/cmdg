package cmdg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gmail "google.golang.org/api/gmail/v1"
)

// slowGets serves message gets slowly enough for them to overlap, and
// records how many were in flight at once and how often each message was
// fetched.
type slowGets struct {
	m        sync.Mutex
	inFlight int
	maxSeen  int
	gets     map[string]int
}

func (s *slowGets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	s.m.Lock()
	s.inFlight++
	s.maxSeen = max(s.maxSeen, s.inFlight)
	s.gets[id]++
	s.m.Unlock()
	time.Sleep(20 * time.Millisecond)
	s.m.Lock()
	s.inFlight--
	s.m.Unlock()
	_ = json.NewEncoder(w).Encode(&gmail.Message{
		Id:      id,
		Payload: &gmail.MessagePart{},
	})
}

// TestPreloadLimitsAndShares preloads 30 messages from 5 goroutines each, as
// the page preload and the message list's redraws do, and checks that each
// message is fetched once and that no more than maxConcurrentGets gets are
// in flight at once.
func TestPreloadLimitsAndShares(t *testing.T) {
	s := &slowGets{gets: map[string]int{}}
	ts := httptest.NewServer(s)
	defer ts.Close()
	conn, err := NewFake(&http.Client{Transport: &redirector{ts.URL}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		msg := NewMessage(conn, fmt.Sprintf("m%d", i))
		for j := 0; j < 5; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := msg.Preload(ctx, LevelMetadata)
				if err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	if s.maxSeen > maxConcurrentGets {
		t.Errorf("%d gets in flight at once, want at most %d",
			s.maxSeen, maxConcurrentGets)
	}
	if len(s.gets) != 30 {
		t.Errorf("fetched %d messages, want 30", len(s.gets))
	}
	for id, n := range s.gets {
		if n != 1 {
			t.Errorf("message %s fetched %d times, want 1", id, n)
		}
	}
}
