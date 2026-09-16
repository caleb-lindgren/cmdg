package main

import (
	"testing"

	"github.com/ThomasHabets/cmdg/internal/customize"
)

// TestCustomKeysDoNotShadowUpstream is the check to look at after
// merging upstream. This fork rebinds a few keys in the open-message
// view, and does it by translating the keypress before cmdg's own
// switch statement sees it — so a binding upstream adds on a key the
// fork already uses would never fire, and nothing at runtime would say
// so. Failing here is not a bug in either side's code: it means the
// local key has to move, or that displacing upstream's new binding was
// the intent and the binding table should say so.
func TestCustomKeysDoNotShadowUpstream(t *testing.T) {
	if got := customize.ShadowedOpenMessageKeys(
		openMessageViewHelp); len(got) != 0 {
		t.Errorf("upstream now binds %q in the open-message view, "+
			"which internal/customize rebinds locally", got)
	}
}
