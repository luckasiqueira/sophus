package storageworker

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"
)

const scanInterval = 24 * time.Hour

var runMu sync.Mutex

func Start(ctx context.Context) {
	go func() {
		RunOnce(time.Now().UTC())
		ticker := time.NewTicker(scanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				RunOnce(now.UTC())
			}
		}
	}()
	go func() {
		runConversationPolicies(time.Now().UTC(), false)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				runConversationPolicies(now.UTC(), false)
			}
		}
	}()
}

func RunOnce(now time.Time) {
	if !runMu.TryLock() {
		return
	}
	defer runMu.Unlock()
	policies, err := repo.ListCompanyStoragePolicies()
	if err != nil {
		log.Printf("storage scan: list companies: %v", err)
		return
	}
	for _, policy := range policies {
		if policy.MediaRetentionDays != nil {
			deleteExpiredMedia(policy.CompanyId, *policy.MediaRetentionDays, now)
		}
		if policy.Operations.ConversationRetentionDays > 0 {
			paths, err := repo.ListExpiredConversationMedia(policy.CompanyId, policy.Operations.ConversationRetentionDays, now)
			if err != nil {
				log.Printf("conversation retention media: company %d: %v", policy.CompanyId, err)
			} else {
				for _, path := range paths {
					if _, deleteErr := media.Delete(policy.CompanyId, media.ScopeMessages, path); deleteErr != nil && !errors.Is(deleteErr, os.ErrNotExist) {
						log.Printf("conversation retention media: company %d path %s: %v", policy.CompanyId, path, deleteErr)
					}
				}
			}
			if _, err := repo.DeleteExpiredConversations(policy.CompanyId, policy.Operations.ConversationRetentionDays, now); err != nil {
				log.Printf("conversation retention: company %d: %v", policy.CompanyId, err)
			}
		}
		usedBytes, fileCount, err := media.ScanCompany(policy.CompanyId)
		if err != nil {
			log.Printf("storage scan: company %d: %v", policy.CompanyId, err)
			continue
		}
		if err := repo.ReconcileCompanyStorage(policy.CompanyId, usedBytes, fileCount, now); err != nil {
			log.Printf("storage scan: persist company %d: %v", policy.CompanyId, err)
		}
	}
}

func runConversationPolicies(now time.Time, includeRetention bool) {
	policies, err := repo.ListCompanyStoragePolicies()
	if err != nil {
		log.Printf("conversation policies: list companies: %v", err)
		return
	}
	for _, policy := range policies {
		closed, err := repo.ApplyConversationInactivityPolicy(policy.CompanyId, policy.Operations.AutoCloseMinutes, now)
		if err != nil {
			log.Printf("conversation inactivity: company %d: %v", policy.CompanyId, err)
			continue
		}
		if closed > 0 {
			sse.NotifyConversations(policy.CompanyId)
		}
		if includeRetention && policy.Operations.ConversationRetentionDays > 0 {
			_, _ = repo.DeleteExpiredConversations(policy.CompanyId, policy.Operations.ConversationRetentionDays, now)
		}
	}
}

func deleteExpiredMedia(companyID, retentionDays int, now time.Time) {
	paths, err := repo.ListExpiredMessageMedia(companyID, retentionDays, now)
	if err != nil {
		log.Printf("storage retention: company %d: %v", companyID, err)
		return
	}
	for _, path := range paths {
		_, err := media.Delete(companyID, media.ScopeMessages, path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("storage retention: delete company %d media %s: %v", companyID, path, err)
			continue
		}
		if err := repo.ClearMessageMediaPath(companyID, path); err != nil {
			log.Printf("storage retention: clear company %d media %s: %v", companyID, path, err)
		}
	}
}
