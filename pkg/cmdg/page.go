package cmdg

import (
	"context"

	log "github.com/sirupsen/logrus"
	gmail "google.golang.org/api/gmail/v1"
)

// Page implements some pagination thingy. TODO: document better.
type Page struct {
	Label string
	Query string

	conn     *CmdG
	Messages []*Message
	Response *gmail.ListMessagesResponse
}

// Next return the next page.
func (p *Page) Next(ctx context.Context) (*Page, error) {
	return p.conn.ListMessages(ctx, p.Label, p.Query, p.Response.NextPageToken)
}

// PreloadSubjects async loads message basic info.
func (p *Page) PreloadSubjects(ctx context.Context) error {
	conc := 100
	sem := make(chan struct{}, conc)
	num := len(p.Response.Messages)
	errs := make([]error, num)
	for n := 0; n < len(p.Response.Messages); n++ {
		n := n
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()

			if err := p.Messages[n].Preload(ctx, LevelMetadata); err != nil {
				errs[n] = err
			}
		}()
	}
	for t := 0; t < conc; t++ {
		sem <- struct{}{}
	}
	// Logged through logrus, which cmdg sends to its log file. The
	// standard log package writes to the terminal, over the UI.
	failed := 0
	var first error
	for _, err := range errs {
		if err != nil {
			if first == nil {
				first = err
			}
			failed++
		}
	}
	if failed > 0 {
		log.Warningf("Preloading subjects: %d of %d failed, first: %v",
			failed, num, first)
	}
	return nil
}
