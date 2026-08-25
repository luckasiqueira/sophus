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
