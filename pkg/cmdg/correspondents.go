package cmdg

import (
	"context"
	"mime"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/html/charset"
	gmail "google.golang.org/api/gmail/v1"
	people "google.golang.org/api/people/v1"
)

const (
	// Gmail allows 15,000 quota units per user per minute (250 a second),
	// and a messages.get costs 5. 25 gets a second uses half of that,
	// leaving the rest for the UI. 2000 messages then take 80 seconds.
	correspondentGetsPerSecond = 25
	correspondentWorkers       = 5

	listPageSize          = 500
	otherContactsPageSize = 1000
)

// Variables rather than constants so that tests can shrink them.
var (
	// How many of the newest messages, sent and received, to read
	// addresses from, and how many of the newest sent messages. The
	// sent ones mostly overlap the first set; the rest reach further
	// back for the same cost, since only sent mail shows who the user
	// emailed. Only messages not already read are fetched, so after the
	// first scan a reload costs the list calls (one per 500 IDs, so 14)
	// plus one get per new message.
	correspondentScanSize = 2000
	sentScanSize          = 5000

	// How many messages to read between updates of the address book, so
	// that suggestions are sorted soon after startup rather than at the
	// end of the first scan. 250 gets take 10 seconds.
	correspondentBatchSize = 250
)

var (
	errScanListFull = errors.New("enough message IDs listed")

	angleAddrRE = regexp.MustCompile(`<([^<>]+)>\s*$`)

	// Decodes RFC 2047 encoded names in any charset, not just the
	// UTF-8, ISO-8859-1 and US-ASCII that net/mail handles by default.
	addressParser = &mail.AddressParser{
		WordDecoder: &mime.WordDecoder{
			CharsetReader: charset.NewReaderLabel,
		},
	}
)

// correspondentScan is the state of the scan of recent messages, kept
// between reloads so that each message is fetched only once.
type correspondentScan struct {
	m       sync.Mutex
	self    string                   // Own address, lowercased.
	scanned map[string]bool          // Message IDs already read.
	found   map[string]correspondent // Keyed by lowercased address.
}

// correspondent is an address found by the scan.
type correspondent struct {
	entry string // Formatted address book entry.
	last  int64  // Date of the newest message, ms since the epoch.
}

// LoadCorrespondents adds to the address book everyone in Google's "Other
// contacts" (people the user has emailed), everyone the newest
// correspondentScanSize messages were sent to or received from, and
// everyone the newest sentScanSize sent messages were sent to. Failing to
// read Other contacts, which needs the contacts.other.readonly scope, is
// logged and does not stop the scan.
func (c *CmdG) LoadCorrespondents(ctx context.Context) error {
	if oc, err := c.GetOtherContacts(ctx); err != nil {
		if !c.otherContactsFailed {
			log.Warningf("Failed to load Other contacts; rerun "+
				"-configure to grant the scope: %v", err)
			c.otherContactsFailed = true
		}
	} else {
		c.m.Lock()
		c.otherContacts = oc
		c.rebuildAddressBook()
		c.m.Unlock()
	}
	return c.scanRecent(ctx)
}

// GetOtherContacts gets the addresses in Google's "Other contacts", in the
// same format as GetContacts.
func (c *CmdG) GetOtherContacts(ctx context.Context) ([]string, error) {
	var ret []string
	add := func(r *people.ListOtherContactsResponse) error {
		for _, p := range r.OtherContacts {
			ret = append(ret, personAddresses(p)...)
		}
		return nil
	}
	err := wrapLogRPC("people.OtherContacts.List", func() error {
		return c.people.OtherContacts.List().
			ReadMask("names,emailAddresses").
			PageSize(otherContactsPageSize).
			Pages(ctx, add)
	}, "")
	return ret, err
}

// scanRecent reads the address headers of those of the newest
// correspondentScanSize messages, and of the newest sentScanSize sent
// messages, that it has not read before. It updates the address book after
// every correspondentBatchSize messages.
func (c *CmdG) scanRecent(ctx context.Context) error {
	s := &c.scan
	s.m.Lock()
	defer s.m.Unlock()
	if s.scanned == nil {
		s.scanned = make(map[string]bool)
		s.found = make(map[string]correspondent)
	}
	if s.self == "" {
		p, err := c.GetProfile(ctx)
		if err != nil {
			return errors.Wrap(err, "getting own address")
		}
		s.self = strings.ToLower(p.EmailAddress)
	}

	ids, err := c.listRecentIDs(ctx, "", correspondentScanSize)
	if err != nil {
		return err
	}
	sent, err := c.listRecentIDs(ctx, "SENT", sentScanSize)
	if err != nil {
		return err
	}
	// Newest first: the sent messages not in the first set are older
	// than all of it.
	var todo []string
	queued := make(map[string]bool)
	for _, id := range append(ids, sent...) {
		if !s.scanned[id] && !queued[id] {
			queued[id] = true
			todo = append(todo, id)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	log.Infof("Scanning %d new messages for addresses", len(todo))

	tick := time.NewTicker(time.Second / correspondentGetsPerSecond)
	defer tick.Stop()
	for b := 0; b < len(todo); b += correspondentBatchSize {
		end := min(b+correspondentBatchSize, len(todo))
		c.scanBatch(ctx, todo[b:end], tick.C)
		c.publishCorrespondents()
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// scanBatch reads the address headers of the messages ids, newest first,
// one per tick, and adds what it finds to c.scan. Caller holds c.scan.m.
func (c *CmdG) scanBatch(ctx context.Context, ids []string,
	tick <-chan time.Time) {
	s := &c.scan
	results := make([][]*mail.Address, len(ids))
	dates := make([]int64, len(ids))
	ok := make([]bool, len(ids))
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < correspondentWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				m, err := c.getAddressHeaders(ctx, ids[i])
				if err != nil {
					log.Warningf("Scanning message %q: %v",
						ids[i], err)
					continue
				}
				results[i] = messageCorrespondents(m)
				dates[i] = m.InternalDate
				ok[i] = true
			}
		}()
	}
feed:
	for i := range ids {
		select {
		case <-tick:
		case <-ctx.Done():
			break feed
		}
		work <- i
	}
	close(work)
	wg.Wait()

	// Newest first, so the first named entry for an address wins.
	for i, as := range results {
		if !ok[i] {
			continue // Retried on the next reload.
		}
		s.scanned[ids[i]] = true
		for _, a := range as {
			k := strings.ToLower(a.Address)
			if k == s.self {
				continue
			}
			// Set the entry unless it already has a name.
			f, seen := s.found[k]
			if !seen || f.entry == k {
				f.entry = formatAddress(a.Name, a.Address)
			}
			if dates[i] > f.last {
				f.last = dates[i]
			}
			s.found[k] = f
		}
	}
}

// publishCorrespondents puts what the scan has found so far into the address
// book. Caller holds c.scan.m.
func (c *CmdG) publishCorrespondents() {
	s := &c.scan
	recent := make([]string, 0, len(s.found))
	lastSeen := make(map[string]int64, len(s.found))
	for k, f := range s.found {
		recent = append(recent, f.entry)
		lastSeen[k] = f.last
	}
	c.m.Lock()
	c.recent = recent
	c.lastSeen = lastSeen
	c.rebuildAddressBook()
	c.m.Unlock()
}

// listRecentIDs lists the IDs of the newest n messages, newest first: those
// with the label, or all of them, sent and received, if label is empty.
func (c *CmdG) listRecentIDs(ctx context.Context, label string, n int) (
	[]string, error) {
	var ids []string
	call := c.gmail.Users.Messages.List(email).
		MaxResults(listPageSize).
		Fields("messages/id,nextPageToken")
	if label != "" {
		call = call.LabelIds(label)
	}
	add := func(r *gmail.ListMessagesResponse) error {
		for _, m := range r.Messages {
			ids = append(ids, m.Id)
		}
		if len(ids) >= n {
			return errScanListFull
		}
		return nil
	}
	err := wrapLogRPC("gmail.Users.Messages.List", func() error {
		return call.Pages(ctx, add)
	}, "email=%q label=%q", email, label)
	if err != nil && err != errScanListFull {
		return nil, err
	}
	if len(ids) > n {
		ids = ids[:n]
	}
	return ids, nil
}

// getAddressHeaders gets only a message's date, labels and address headers.
func (c *CmdG) getAddressHeaders(ctx context.Context, id string) (
	*gmail.Message, error) {
	var m *gmail.Message
	err := wrapLogRPC("gmail.Users.Messages.Get", func() (err error) {
		m, err = c.gmail.Users.Messages.Get(email, id).
			Format(string(LevelMetadata)).
			MetadataHeaders("From", "To", "Cc", "Bcc").
			Fields("internalDate,labelIds,payload/headers").
			Context(ctx).Do()
		return
	}, "email=%q id=%q", email, id)
	return m, err
}

// messageCorrespondents returns who a message was received from or, for a
// message the user sent, who it was sent to. Other recipients of a received
// message are left out: they are often mailing lists rather than people.
func messageCorrespondents(m *gmail.Message) []*mail.Address {
	if m == nil || m.Payload == nil {
		return nil
	}
	want := map[string]bool{"from": true}
	for _, l := range m.LabelIds {
		if l == "SENT" {
			want = map[string]bool{
				"to": true, "cc": true, "bcc": true,
			}
		}
	}
	var ret []*mail.Address
	for _, h := range m.Payload.Headers {
		if !want[strings.ToLower(h.Name)] || h.Value == "" {
			continue
		}
		as, err := addressParser.ParseList(h.Value)
		if err != nil {
			log.Debugf("Unparseable %s header %q: %v",
				h.Name, h.Value, err)
			continue
		}
		ret = append(ret, as...)
	}
	return ret
}

// addressKey returns the lowercased email address in an address book entry.
func addressKey(entry string) string {
	if a, err := addressParser.Parse(entry); err == nil {
		return strings.ToLower(a.Address)
	}
	if m := angleAddrRE.FindStringSubmatch(entry); m != nil {
		return strings.ToLower(strings.TrimSpace(m[1]))
	}
	return strings.ToLower(strings.TrimSpace(entry))
}

// mergeAddresses returns all of contacts, followed by the entries of each
// further list whose address is not in an earlier one, sorted the way
// GetContacts sorts. Duplicates within contacts are kept, as before.
func mergeAddresses(contacts []string, more ...[]string) []string {
	seen := make(map[string]bool)
	ret := append([]string{}, contacts...)
	for _, e := range contacts {
		seen[addressKey(e)] = true
	}
	for _, l := range more {
		for _, e := range l {
			k := addressKey(e)
			if !seen[k] {
				seen[k] = true
				ret = append(ret, e)
			}
		}
	}
	sort.Slice(ret, func(i, j int) bool {
		return strings.TrimLeft(ret[i], `"`) <
			strings.TrimLeft(ret[j], `"`)
	})
	return ret
}

// sortByLastSeen moves the entries of book whose address has a date in
// lastSeen to the front, newest first, and leaves the rest after them in
// their existing order. Equal dates also keep their order.
func sortByLastSeen(book []string, lastSeen map[string]int64) []string {
	type dated struct {
		entry string
		last  int64
	}
	ds := make([]dated, len(book))
	for i, e := range book {
		ds[i] = dated{e, lastSeen[addressKey(e)]}
	}
	sort.SliceStable(ds, func(i, j int) bool {
		return ds[i].last > ds[j].last
	})
	for i, d := range ds {
		book[i] = d.entry
	}
	return book
}

// rebuildAddressBook recomputes the list Contacts returns: everyone the scan
// found, most recently emailed first, then everyone else alphabetically.
// Caller holds c.m.
func (c *CmdG) rebuildAddressBook() {
	c.addressBook = sortByLastSeen(
		mergeAddresses(c.contacts, c.otherContacts, c.recent),
		c.lastSeen)
}
