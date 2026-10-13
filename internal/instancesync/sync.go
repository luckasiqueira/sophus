package instancesync

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"sophus/internal/repo"
	"sophus/internal/repo/instances"
	"sophus/pkg/http/middlewares/sse"
)

const (
	syncInterval       = 2 * time.Minute
	reconnectInterval  = 5 * time.Minute
	maxConcurrentSyncs = 2
)

var syncMu sync.Mutex
var companyMu sync.Mutex
var reconnectMu sync.Mutex

var pictureSubscriptions = struct {
	sync.Mutex
	until map[string]time.Time
}{until: make(map[string]time.Time)}

func syncPictureSubscription(instance *instances.InstanceEVO) {
	key := instance.InstanceID.String() + ":" + instance.APIToken
	pictureSubscriptions.Lock()
	due := !time.Now().Before(pictureSubscriptions.until[key])
	if due {
		pictureSubscriptions.until[key] = time.Now().Add(time.Minute)
	}
	pictureSubscriptions.Unlock()
	if !due {
		return
	}
	if err := instance.EnsurePictureSubscription(); err != nil {
		log.Printf("failed to enable picture webhook: instance=%s error=%v", instance.InstanceID, err)
		return
	}
	pictureSubscriptions.Lock()
	pictureSubscriptions.until[key] = time.Now().Add(15 * time.Minute)
	pictureSubscriptions.Unlock()
}

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
	go func() {
		reconnectAll()
		ticker := time.NewTicker(reconnectInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				reconnectAll()
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
	processConnections(connections, SyncConnection)
}

func processConnections(connections []repo.ConnectionEVO, process func(repo.ConnectionEVO)) {
	var group sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrentSyncs)
	for _, connection := range connections {
		connection := connection
		group.Add(1)
		go func() {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			process(connection)
		}()
	}
	group.Wait()
}

func reconnectAll() {
	if !reconnectMu.TryLock() {
		return
	}
	defer reconnectMu.Unlock()

	connections, err := repo.GetConnectionList()
	if err != nil {
		log.Printf("failed to list connections for automatic reconnect: %v", err)
		return
	}
	candidates := automaticReconnectCandidates(connections)
	if len(candidates) == 0 {
		return
	}
	processConnections(candidates, func(connection repo.ConnectionEVO) {
		reconnectConnection(connection)
	})
}

func automaticReconnectCandidates(connections []repo.ConnectionEVO) []repo.ConnectionEVO {
	candidates := make([]repo.ConnectionEVO, 0, len(connections))
	for _, connection := range connections {
		if connection.Status == repo.ConnectionStatusConnected {
			candidates = append(candidates, connection)
		}
	}
	return candidates
}

func reconnectConnection(connection repo.ConnectionEVO) {
	instance := instances.InstanceEVO{
		InstanceID: connection.InstanceID,
		APIToken:   connection.EvolutionAPIKey(),
	}
	status, err := instance.GetStatus()
	if err != nil {
		log.Printf("failed to verify Evolution GO instance before automatic reconnect: connection=%d error=%v", connection.Id, err)
		return
	}
	if status.Connected {
		syncPictureSubscription(&instance)
		reconcileConnection(connection, status)
		return
	}
	if err := instance.Reconnect(); err != nil {
		if errors.Is(err, instances.ErrReconnectInProgress) {
			return
		}
		log.Printf("failed to request automatic Evolution GO reconnect: connection=%d error=%v", connection.Id, err)
		return
	}
	log.Printf("automatic Evolution GO reconnect requested: connection=%d", connection.Id)
}

func SyncConnection(connection repo.ConnectionEVO) {
	instance := instances.InstanceEVO{InstanceID: connection.InstanceID, APIToken: connection.EvolutionAPIKey()}
	status, err := instance.GetStatus()
	if err != nil {
		log.Printf("failed to query Evolution GO status: connection=%d error=%v", connection.Id, err)
		return
	}
	if status.Connected {
		syncPictureSubscription(&instance)
	}
	// Keep the last known connected state as the automatic reconnect intent.
	// Explicit disconnected/loggedout webhooks still disable reconnection.
	if !shouldReconcileStatus(connection.Status, status) {
		return
	}
	reconcileConnection(connection, status)
}

func shouldReconcileStatus(current string, status instances.Status) bool {
	return status.Connected || current != repo.ConnectionStatusConnected
}

func reconcileConnection(connection repo.ConnectionEVO, status instances.Status) {
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
