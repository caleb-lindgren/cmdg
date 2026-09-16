package main

import (
	"testing"

	"github.com/ThomasHabets/cmdg/internal/customize"
)

// TestCustomKeysDoNotShadowUpstream is the check to look at after
// merging upstream. This fork rebinds keys by translating the keypress
// before cmdg's own switch statement sees it, so a binding upstream
// adds on a key the fork already claims would never fire, and nothing
// at runtime would say so. Failing here is not a bug in either side's
// code: it means the local key has to move, or that displacing
// upstream's new binding was the intent and internal/customize's
// binding table should record that.
func TestCustomKeysDoNotShadowUpstream(t *testing.T) {
	om := customize.ShadowedOpenMessageKeys(openMessageViewHelp)
	ml := customize.ShadowedMessageListKeys(messageListViewHelp)
	for _, tc := range []struct {
		view string
		got  []string
	}{
		{"open message", om},
		{"message list", ml},
	} {
		if len(tc.got) != 0 {
			t.Errorf("upstream now binds %q in the %s view, "+
				"which internal/customize claims locally",
				tc.got, tc.view)
		}
	}
}
