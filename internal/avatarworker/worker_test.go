package avatarworker

import (
	"errors"
	"fmt"
	"testing"

	"sophus/internal/repo"
)

func TestAvatarLookupResultDistinguishesRemovalFromFailure(t *testing.T) {
	for _, test := range []struct {
		name, candidate, want string
		err                   error
		retry                 bool
	}{
		{name: "new photo", candidate: " https://pps.whatsapp.net/new.jpg ", want: "https://pps.whatsapp.net/new.jpg"},
		{name: "removed photo", err: fmt.Errorf("lookup: %w", repo.ErrAvatarNotFound)},
		{name: "temporary lookup", err: repo.ErrTemporaryAvatarLookup, retry: true},
		{name: "failed request", err: errors.New("connection unavailable"), retry: true},
		{name: "missing URL", retry: true},
		{name: "invalid URL", candidate: "http://invalid/photo.jpg", retry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, retry := avatarLookupResult(test.candidate, test.err)
			if value != test.want || retry != test.retry {
				t.Fatalf("result = (%q, %v), want (%q, %v)", value, retry, test.want, test.retry)
			}
		})
	}
}
