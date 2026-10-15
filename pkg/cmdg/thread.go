package cmdg

import (
	"context"
	"sync"

	"github.com/pkg/errors"
	gmail "google.golang.org/api/gmail/v1"
)

// ThreadMessages returns the messages of the thread id, oldest first, each
// loaded in full. It lists the thread's message IDs with one request and
// loads each message as Preload does, so that a message the message list
// already holds is the same *Message, and that each one gets the body
// rendering, GPG checks and attachment annotation a message opened on its
// own gets.
func (c *CmdG) ThreadMessages(ctx context.Context,
	id ThreadID) ([]*Message, error) {
	const fields = "messages(id,threadId,labelIds)"
	var th *gmail.Thread
	err := wrapLogRPC("gmail.Users.Threads.Get", func() (err error) {
		th, err = c.gmail.Users.Threads.Get(email, string(id)).
			Format(string(LevelMinimal)).
			Fields(fields).
			Context(ctx).
			Do()
		return
	}, "email=%q threadID=%q fields=%q", email, id, fields)
	if err != nil {
		return nil, errors.Wrapf(err, "getting thread %q", id)
	}

	msgs := make([]*Message, len(th.Messages))
	errs := make([]error, len(th.Messages))
	var wg sync.WaitGroup
	for n, m := range th.Messages {
		msgs[n] = NewMessage(c, m.Id)
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			errs[n] = msgs[n].Preload(ctx, LevelFull)
		}(n)
	}
	wg.Wait()
	for n, err := range errs {
		if err != nil {
			return nil, errors.Wrapf(err,
				"loading message %q of thread %q",
				msgs[n].ID, id)
		}
	}
	return msgs, nil
}

// ModifyThread adds the labels add to, and removes the labels remove from,
// every message in the thread id.
func (c *CmdG) ModifyThread(ctx context.Context, id ThreadID,
	add, remove []string) error {
	return wrapLogRPC("gmail.Users.Threads.Modify", func() error {
		_, err := c.gmail.Users.Threads.Modify(email, string(id),
			&gmail.ModifyThreadRequest{
				AddLabelIds:    add,
				RemoveLabelIds: remove,
			}).Context(ctx).Do()
		return err
	}, "email=%q threadID=%q add=%v remove=%v", email, id, add, remove)
}

// maxConcurrentThreadGets is how many ThreadLabels requests may be in
// flight at once. They have a limit of their own rather than taking
// getSlots, so that the message list's counts are not held up behind the
// thousands of gets of the address scan at startup; a small one, as
// together with getSlots they count towards Gmail's undocumented limit
// on concurrent requests.
const maxConcurrentThreadGets = 4

var threadGetSlots = make(chan struct{}, maxConcurrentThreadGets)

// ThreadLabels returns the label IDs of each message in the thread id,
// oldest first, with one request for message IDs and labels only.
func (c *CmdG) ThreadLabels(ctx context.Context,
	id ThreadID) ([][]string, error) {
	_, ls, err := c.ThreadMessageLabels(ctx, id)
	return ls, err
}

// ThreadMessageLabels returns the ID and the label IDs of each message in
// the thread id, oldest first, with one request for those only.
func (c *CmdG) ThreadMessageLabels(ctx context.Context,
	id ThreadID) ([]string, [][]string, error) {
	select {
	case threadGetSlots <- struct{}{}:
		defer func() { <-threadGetSlots }()
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	const fields = "messages(id,labelIds)"
	var th *gmail.Thread
	err := wrapLogRPC("gmail.Users.Threads.Get", func() (err error) {
		th, err = c.gmail.Users.Threads.Get(email, string(id)).
			Format(string(LevelMinimal)).
			Fields(fields).
			Context(ctx).
			Do()
		return
	}, "email=%q threadID=%q fields=%q", email, id, fields)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "getting thread %q", id)
	}
	ids := make([]string, len(th.Messages))
	ls := make([][]string, len(th.Messages))
	for n, m := range th.Messages {
		ids[n] = m.Id
		ls[n] = m.LabelIds
	}
	return ids, ls, nil
}
