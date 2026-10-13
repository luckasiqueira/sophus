package objectcache

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func testCache(t *testing.T) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, DialerRetries: 1, ContextTimeoutEnabled: true, DialTimeout: 100 * time.Millisecond, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { client.Close() })
	return &Cache{client: client}, server
}

func TestCacheHitExpiryAndPermissionInvalidation(t *testing.T) {
	cache, server := testCache(t)
	var value struct {
		Active bool `json:"active"`
	}
	active, loads := true, 0
	load := func() error { loads++; value.Active = active; return nil }
	for i := 0; i < 2; i++ {
		if err := cache.Get("session", "7", time.Second, &value, load); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 1 || !value.Active {
		t.Fatalf("cache missed: loads=%d value=%+v", loads, value)
	}
	active = false
	cache.Invalidate("session")
	if err := cache.Get("session", "7", time.Second, &value, load); err != nil {
		t.Fatal(err)
	}
	if loads != 2 || value.Active {
		t.Fatal("revoked permission remained cached")
	}
	server.FastForward(2 * time.Second)
	if err := cache.Get("session", "7", time.Second, &value, load); err != nil {
		t.Fatal(err)
	}
	if loads != 3 {
		t.Fatal("expired object reused")
	}
}

func TestReadInFlightCannotRepopulateInvalidatedScope(t *testing.T) {
	cache, _ := testCache(t)
	var value int
	if err := cache.Get("access", "8", time.Minute, &value, func() error { value = 1; cache.Invalidate("access"); return nil }); err != nil {
		t.Fatal(err)
	}
	loaded := false
	if err := cache.Get("access", "8", time.Minute, &value, func() error { loaded = true; value = 2; return nil }); err != nil {
		t.Fatal(err)
	}
	if !loaded || value != 2 {
		t.Fatal("in-flight old value repopulated current cache")
	}
}

func TestRedisOutageFallsBackAndPreservesPendingInvalidation(t *testing.T) {
	cache, server := testCache(t)
	server.Close()
	cache.Invalidate("session")
	if _, ok := cache.dirty.Load("session"); !ok {
		t.Fatal("failed invalidation lost")
	}
	var value int
	loads := 0
	for i := 0; i < 2; i++ {
		if err := cache.Get("session", "7", time.Minute, &value, func() error { loads++; value = 3; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 2 || value != 3 {
		t.Fatal("outage did not fall back to source")
	}
	if err := cache.Get("session", "missing", time.Minute, &value, func() error { return errors.New("source unavailable") }); err == nil {
		t.Fatal("source errors were hidden")
	}
}

func TestConcurrentCacheMissesShareOneSourceRead(t *testing.T) {
	cache, _ := testCache(t)
	var warm int
	if err := cache.Get("warmup", "connection", time.Minute, &warm, func() error { warm = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	var loads atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			var value int
			if err := cache.Get("session", "same-agent", time.Minute, &value, func() error { loads.Add(1); time.Sleep(20 * time.Millisecond); value = 7; return nil }); err != nil {
				t.Error(err)
			}
			if value != 7 {
				t.Errorf("shared load lost object: %d", value)
			}
		}()
	}
	group.Wait()
	if loads.Load() != 1 {
		t.Fatalf("source reads=%d, want 1", loads.Load())
	}
}

func TestHistorySnapshotLivesSixHoursAndUpdatesAtomically(t *testing.T) {
	cache, server := testCache(t)
	value := 0
	loads := 0
	load := func() error { loads++; value = 1; return nil }
	if err := cache.Get("history-1-2", "snapshot", 6*time.Hour, &value, load); err != nil {
		t.Fatal(err)
	}
	server.FastForward(5 * time.Hour)
	if err := cache.Get("history-1-2", "snapshot", 6*time.Hour, &value, load); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatal("history expired before six hours")
	}
	updated, err := cache.UpdateJSON("history-1-2", "snapshot", 6*time.Hour, func(data []byte) ([]byte, error) {
		var n int
		if err := json.Unmarshal(data, &n); err != nil {
			return nil, err
		}
		return json.Marshal(n + 1)
	})
	if err != nil || !updated {
		t.Fatalf("update failed: %v %v", updated, err)
	}
	if err := cache.Get("history-1-2", "snapshot", 6*time.Hour, &value, load); err != nil {
		t.Fatal(err)
	}
	if value != 2 || loads != 1 {
		t.Fatal("updated snapshot not served from Redis")
	}
	server.FastForward(6*time.Hour + time.Second)
	if err := cache.Get("history-1-2", "snapshot", 6*time.Hour, &value, load); err != nil {
		t.Fatal(err)
	}
	if loads != 2 {
		t.Fatal("expired history did not reload from source")
	}
}

func TestColdHistoryLoadDoesNotOverwriteNewerSnapshot(t *testing.T) {
	cache, server := testCache(t)
	var value int
	if err := cache.Get("history", "snapshot", time.Hour, &value, func() error { value = 1; server.Set(prefix+"history:0:snapshot", "2"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := cache.Get("history", "snapshot", time.Hour, &value, func() error { t.Fatal("source reread despite newer cache"); return nil }); err != nil {
		t.Fatal(err)
	}
	if value != 2 {
		t.Fatal("old loader overwrote newer history")
	}
}

func TestDeletedHistoryPurgesCachedVersions(t *testing.T) {
	cache, server := testCache(t)
	value := 1
	if err := cache.Get("history-1-2", "snapshot", 6*time.Hour, &value, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	cache.Invalidate("history-1-2")
	if err := cache.Get("history-1-2", "snapshot", 6*time.Hour, &value, func() error { value = 2; return nil }); err != nil {
		t.Fatal(err)
	}
	cache.Delete("history-1-2", "snapshot")
	if server.Exists(prefix+"history-1-2:0:snapshot") || server.Exists(prefix+"history-1-2:1:snapshot") {
		t.Fatal("deleted transcript remained in Redis")
	}
}
