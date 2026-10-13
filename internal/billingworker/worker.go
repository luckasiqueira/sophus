package billingworker

import (
	"context"
	"log"
	"time"

	"sophus/internal/repo"
)

func Start(ctx context.Context) {
	go func() {
		run := func() {
			created, err := repo.GenerateDueRenewalPayments(ctx, time.Now().UTC())
			if err != nil {
				log.Printf("renewal invoice generation failed: %v", err)
			}
			if created > 0 {
				log.Printf("renewal invoices created: %d", created)
			}
		}
		run()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
