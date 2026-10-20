package cmdg

import (
	"errors"
	"net/http"
	"testing"

	pkgerrors "github.com/pkg/errors"
	"google.golang.org/api/googleapi"
)

func TestIsRateLimited(t *testing.T) {
	const forbidden = http.StatusForbidden
	reason := func(code int, r string) error {
		return &googleapi.Error{Code: code,
			Errors: []googleapi.ErrorItem{{Reason: r}}}
	}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"none", nil, false},
		{"not from Gmail", errors.New("no"), false},
		{"too many in flight", &googleapi.Error{
			Code: http.StatusTooManyRequests}, true},
		// As Gmail answered once the quota was spent.
		{"quota", reason(forbidden, "rateLimitExceeded"), true},
		{"user quota", reason(forbidden, "userRateLimitExceeded"),
			true},
		{"wrapped", pkgerrors.Wrap(
			reason(forbidden, "rateLimitExceeded"),
			"listing messages"), true},
		{"forbidden otherwise",
			reason(forbidden, "insufficientPermissions"), false},
		{"not found", reason(http.StatusNotFound, "notFound"), false},
	} {
		if got := IsRateLimited(tc.err); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
