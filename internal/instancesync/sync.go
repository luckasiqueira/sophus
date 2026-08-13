package instancesync

import (
	"context"
	"log"
	"sync"
	"time"

	"sophus/internal/repo"
	"sophus/internal/repo/instances"
	"sophus/pkg/http/middlewares/sse"
)

const syncInterval = 15 * time.Second

var syncMu sync.Mutex
var companyMu sync.Mutex

func Start(ctx context.Context) {
	go func() {
		SyncAll()
		ticker := time.NewTicker(syncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				SyncAll()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func SyncAll() {
	if !syncMu.TryLock() {
		return
	}
	defer syncMu.Unlock()

	connections, err := repo.GetConnectionList()
	if err != nil {
		log.Printf("failed to list connections for status sync: %v", err)
		return
	}
	syncConnections(connections)
}

func SyncCompany(companyID int) {
	companyMu.Lock()
	defer companyMu.Unlock()
	connections, err := repo.GetConnectionListByCompany(companyID)
	if err != nil {
		log.Printf("failed to list company connections for status sync: company=%d error=%v", companyID, err)
		return
	}
	syncConnections(connections)
}

func syncConnections(connections []repo.ConnectionEVO) {
	var group sync.WaitGroup
	semaphore := make(chan struct{}, 5)
	for _, connection := range connections {
		connection := connection
		group.Add(1)
		go func() {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			SyncConnection(connection)
		}()
	}
	group.Wait()
}

func SyncConnection(connection repo.ConnectionEVO) {
	instance := instances.InstanceEVO{InstanceID: connection.InstanceID, APIToken: connection.EvolutionAPIKey()}
	status, err := instance.GetStatus()
	if err != nil {
		log.Printf("failed to query Evolution GO status: connection=%d error=%v", connection.Id, err)
		return
	}
	changed, err := repo.ReconcileConnectionStatus(connection.Id, status.State, status.Number)
	if err != nil {
		log.Printf("failed to reconcile connection status: connection=%d status=%s error=%v", connection.Id, status.State, err)
		return
	}
	if changed {
		log.Printf("connection status reconciled: connection=%d status=%s", connection.Id, status.State)
		sse.NotifyInstances(connection.CompanyID)
	}
}
