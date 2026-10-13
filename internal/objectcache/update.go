package objectcache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrUnavailable = errors.New("object cache unavailable")

func Enabled() bool { return shared().client != nil }

func Exists(scope, key string) (bool, error) {
	c := shared()
	if !c.ready(scope) {
		return false, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	version, err := c.client.Get(ctx, prefix+scope+":revision").Result()
	if err == redis.Nil {
		version, err = "0", nil
	}
	if err != nil {
		c.failed()
		return false, ErrUnavailable
	}
	count, err := c.client.Exists(ctx, prefix+scope+":"+version+":"+key).Result()
	if err != nil {
		c.failed()
		return false, ErrUnavailable
	}
	return count > 0, nil
}

// UpdateJSON changes an existing snapshot atomically. Missing snapshots are
// reported separately so callers can initialize them from authoritative data.
func UpdateJSON(scope, key string, ttl time.Duration, change func([]byte) ([]byte, error)) (bool, error) {
	return shared().UpdateJSON(scope, key, ttl, change)
}

func (c *Cache) UpdateJSON(scope, key string, ttl time.Duration, change func([]byte) ([]byte, error)) (bool, error) {
	if !c.ready(scope) {
		return false, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	version, err := c.client.Get(ctx, prefix+scope+":revision").Result()
	if err == redis.Nil {
		version, err = "0", nil
	}
	if err != nil {
		c.failed()
		c.Invalidate(scope)
		return false, ErrUnavailable
	}
	objectKey := prefix + scope + ":" + version + ":" + key
	updated := false
	for attempt := 0; attempt < 3; attempt++ {
		err = c.client.Watch(ctx, func(tx *redis.Tx) error {
			current, err := tx.Get(ctx, prefix+scope+":revision").Result()
			if err == redis.Nil {
				current, err = "0", nil
			}
			if err != nil {
				return err
			}
			if current != version {
				return redis.TxFailedErr
			}
			data, err := tx.Get(ctx, objectKey).Bytes()
			if err == redis.Nil {
				return nil
			}
			if err != nil {
				return err
			}
			data, err = change(data)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, objectKey, data, ttl)
				pipe.SAdd(ctx, prefix+scope+":keys", objectKey)
				pipe.Expire(ctx, prefix+scope+":keys", ttl)
				return nil
			})
			updated = err == nil
			return err
		}, objectKey, prefix+scope+":revision")
		if err != redis.TxFailedErr {
			break
		}
	}
	if err != nil {
		c.failed()
		c.Invalidate(scope)
		return false, err
	}
	return updated, nil
}

func Delete(scope, key string) {
	shared().Delete(scope, key)
}

func (c *Cache) Delete(scope, key string) {
	if !c.ready(scope) {
		c.Invalidate(scope)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := redis.NewScript(`
local keys = redis.call('SMEMBERS', KEYS[2])
for _, key in ipairs(keys) do redis.call('DEL', key) end
local version = redis.call('GET', KEYS[1]) or '0'
redis.call('DEL', ARGV[1] .. version .. ':' .. ARGV[2])
redis.call('DEL', KEYS[2])
redis.call('INCR', KEYS[1])
return 1
`).Run(ctx, c.client, []string{prefix + scope + ":revision", prefix + scope + ":keys"}, prefix+scope+":", key).Result()
	if err != nil {
		c.failed()
		c.Invalidate(scope)
	}
}
