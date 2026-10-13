package avatarworker

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"
)

func Start(ctx context.Context) {
	go func() {
		const retryDelay = 2 * time.Minute
		cooldown := make(map[int]time.Time)
		for {
			refresh, err := repo.NextContactAvatarRefresh()
			if errors.Is(err, sql.ErrNoRows) {
				if !wait(ctx, 2*time.Second) {
					return
				}
				continue
			}
			if err != nil {
				log.Printf("failed to load pending contact avatar: %v", err)
				if !wait(ctx, 5*time.Second) {
					return
				}
				continue
			}
			if until := cooldown[refresh.ConnectionID]; time.Now().Before(until) {
				if err := repo.RetryContactAvatarRefresh(refresh.ContactID, refresh.RequestedAt, time.Until(until)); err != nil && !errors.Is(err, sql.ErrNoRows) {
					log.Printf("failed to defer contact avatar: contact=%d error=%v", refresh.ContactID, err)
					if !wait(ctx, 5*time.Second) {
						return
					}
				}
				continue
			}
			avatarURL := ""
			shouldRetry := false
			connection, connectionErr := repo.GetConnectionById(refresh.ConnectionID)
			if connectionErr != nil {
				shouldRetry = true
				log.Printf("contact avatar skipped: contact=%d connection=%d error=%v", refresh.ContactID, refresh.ConnectionID, connectionErr)
			} else {
				candidate, fetchErr := repo.GetWhatsAppAvatarForConnection(refresh.Number, connection)
				avatarURL, shouldRetry = avatarLookupResult(candidate, fetchErr)
				if fetchErr != nil {
					log.Printf("contact avatar lookup failed: contact=%d connection=%d error=%v", refresh.ContactID, refresh.ConnectionID, fetchErr)
				} else if shouldRetry {
					log.Printf("contact avatar lookup returned an invalid URL: contact=%d connection=%d", refresh.ContactID, refresh.ConnectionID)
				}
			}
			if shouldRetry {
				cooldown[refresh.ConnectionID] = time.Now().Add(retryDelay)
				if err := repo.RetryContactAvatarRefresh(refresh.ContactID, refresh.RequestedAt, retryDelay); err != nil {
					if !errors.Is(err, sql.ErrNoRows) {
						log.Printf("failed to schedule avatar retry: contact=%d error=%v", refresh.ContactID, err)
					}
				} else {
					log.Printf("contact avatar retry scheduled: contact=%d connection=%d delay=2m", refresh.ContactID, refresh.ConnectionID)
				}
				continue
			}
			if err := repo.CompleteContactAvatarRefresh(refresh.ContactID, refresh.RequestedAt, avatarURL); err != nil {
				if !errors.Is(err, sql.ErrNoRows) {
					log.Printf("failed to save contact avatar: contact=%d error=%v", refresh.ContactID, err)
				}
			} else {
				sse.NotifyContactAvatar(refresh.CompanyID, refresh.ContactID)
				if avatarURL != "" {
					log.Printf("contact avatar updated: contact=%d connection=%d", refresh.ContactID, refresh.ConnectionID)
				}
			}
			if !wait(ctx, 500*time.Millisecond) {
				return
			}
		}
	}()
}

func avatarLookupResult(candidate string, err error) (string, bool) {
	if errors.Is(err, repo.ErrAvatarNotFound) {
		return "", false
	}
	if err != nil || !validAvatarURL(candidate) {
		return "", true
	}
	return strings.TrimSpace(candidate), false
}

func validAvatarURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

func wait(ctx context.Context, duration time.Duration) bool {
	select {
	case <-time.After(duration):
		return true
	case <-ctx.Done():
		return false
	}
}
