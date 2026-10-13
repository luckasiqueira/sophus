// Package objectcache provides a Redis cache-aside layer. PostgreSQL remains
// authoritative; an unavailable Redis is bypassed with a short circuit breaker.
package objectcache

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"sophus/utils/env"
)

const prefix = "sophus:objects:v1:"

type Cache struct {
	client           *redis.Client
	unavailableUntil atomic.Int64
	invalidations    atomic.Uint64
	dirty            sync.Map
	loads            singleflight.Group
}

var defaultCache *Cache
var initialize sync.Once

func shared() *Cache {
	initialize.Do(func() {
		address := strings.TrimSpace(env.Backend["REDIS_ADDR"])
		defaultCache = &Cache{}
		if address == "" {
			return
		}
		database, err := strconv.Atoi(strings.TrimSpace(env.Backend["REDIS_DB"]))
		if err != nil && strings.TrimSpace(env.Backend["REDIS_DB"]) != "" {
			return
		}
		defaultCache.client = redis.NewClient(&redis.Options{Addr: address, Password: env.Backend["REDIS_PASSWORD"], DB: database,
			DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond,
			PoolTimeout: 100 * time.Millisecond, PoolSize: 12, MaxRetries: -1, DialerRetries: 1, ContextTimeoutEnabled: true})
	})
	return defaultCache
}

func Get(scope, key string, ttl time.Duration, value any, load func() error) error {
	return shared().Get(scope, key, ttl, value, load)
}

func Invalidate(scope string) { shared().Invalidate(scope) }

func (c *Cache) failed() { c.unavailableUntil.Store(time.Now().Add(30 * time.Second).UnixNano()) }

func (c *Cache) ready(scope string) bool {
	if c.client == nil || time.Now().UnixNano() < c.unavailableUntil.Load() {
		return false
	}
	if generation, dirty := c.dirty.Load(scope); dirty {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		if err := c.client.Incr(ctx, prefix+scope+":revision").Err(); err != nil {
			c.failed()
			return false
		}
		c.dirty.CompareAndDelete(scope, generation)
	}
	return true
}

var storeIfCurrent = redis.NewScript(`
local current = redis.call('GET', KEYS[1]) or '0'
if current ~= ARGV[1] then return 0 end
if redis.call('EXISTS', KEYS[2]) == 1 then return 0 end
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[3])
redis.call('SADD', KEYS[3], KEYS[2])
redis.call('PEXPIRE', KEYS[3], ARGV[3])
return 1
`)

func (c *Cache) Get(scope, key string, ttl time.Duration, value any, load func() error) error {
	if !c.ready(scope) {
		return load()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	version, err := c.client.Get(ctx, prefix+scope+":revision").Result()
	if err == redis.Nil {
		version, err = "0", nil
	}
	if err != nil {
		cancel()
		c.failed()
		return load()
	}
	objectKey := prefix + scope + ":" + version + ":" + key
	data, err := c.client.Get(ctx, objectKey).Bytes()
	cancel()
	if err == nil {
		if json.Unmarshal(data, value) == nil {
			return nil
		}
		c.Delete(scope, key)
		return c.Get(scope, key, ttl, value, load)
	}
	if err != nil && err != redis.Nil {
		c.failed()
		return load()
	}
	loaded, err, _ := c.loads.Do(objectKey, func() (any, error) {
		// Another request may have populated the object while this one waited.
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		cached, cacheErr := c.client.Get(ctx, objectKey).Bytes()
		cancel()
		if cacheErr == nil && json.Unmarshal(cached, value) == nil {
			return cached, nil
		}
		if err := load(); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		if _, err := storeIfCurrent.Run(ctx, c.client, []string{prefix + scope + ":revision", objectKey, prefix + scope + ":keys"}, version, encoded, ttl.Milliseconds()).Result(); err != nil {
			c.failed()
		}
		return encoded, nil
	})
	if err != nil {
		return err
	}
	return json.Unmarshal(loaded.([]byte), value)
}

// Versioned keys prevent a read in flight from repopulating a cache invalidated
// by a permission change. Pending invalidations are retried before cache reuse.
func (c *Cache) Invalidate(scope string) {
	if c.client == nil {
		return
	}
	generation := c.invalidations.Add(1)
	c.dirty.Store(scope, generation)
	if time.Now().UnixNano() < c.unavailableUntil.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := c.client.Incr(ctx, prefix+scope+":revision").Err(); err != nil {
		c.failed()
		return
	}
	c.dirty.CompareAndDelete(scope, generation)
}
