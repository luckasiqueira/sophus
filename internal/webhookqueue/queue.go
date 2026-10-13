package webhookqueue

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"sophus/utils/env"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	streamName       = "sophus:webhooks:messages"
	deadLetterStream = "sophus:webhooks:messages:dead"
	consumerGroup    = "sophus"
	maxAttempts      = 5
)

var enqueueScript = redis.NewScript(`
if redis.call('SET', KEYS[2], '1', 'NX', 'EX', 604800) then
  return redis.call('XADD', KEYS[1], '*', 'connectionId', ARGV[1], 'payload', ARGV[2], 'attempts', '0')
end
return ''
`)

type Queue struct {
	client   *redis.Client
	consumer string
}

func New() (*Queue, error) {
	address := strings.TrimSpace(env.Backend["REDIS_ADDR"])
	if address == "" {
		return nil, errors.New("REDIS_ADDR não configurado")
	}
	database, err := strconv.Atoi(strings.TrimSpace(env.Backend["REDIS_DB"]))
	if err != nil && strings.TrimSpace(env.Backend["REDIS_DB"]) != "" {
		return nil, errors.New("REDIS_DB inválido")
	}
	client := redis.NewClient(&redis.Options{
		Addr:         address,
		Password:     env.Backend["REDIS_PASSWORD"],
		DB:           database,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  7 * time.Second,
		WriteTimeout: 5 * time.Second,
		PoolSize:     6,
		MinIdleConns: 1,
	})
	hostname, _ := os.Hostname()
	return &Queue{client: client, consumer: hostname + "-" + uuid.NewString()}, nil
}

func (q *Queue) Ping(ctx context.Context) error {
	return q.client.Ping(ctx).Err()
}

func (q *Queue) Close() error {
	return q.client.Close()
}

func (q *Queue) Enqueue(ctx context.Context, connectionID int, payload []byte) error {
	compressed, err := compress(payload)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	dedupeKey := "sophus:webhooks:dedupe:" + digest
	return enqueueScript.Run(ctx, q.client, []string{streamName, dedupeKey}, connectionID, compressed).Err()
}

func (q *Queue) Start(ctx context.Context, handler func(int, []byte) error) {
	go func() {
		if count, err := q.ReplayGroupMetadataTimeouts(ctx); err != nil {
			log.Printf("group webhook recovery unavailable: %v", err)
		} else if count > 0 {
			log.Printf("group webhooks recovered from dead letter: count=%d", count)
		}
		q.consume(ctx, handler)
	}()
}

var replayGroupTimeoutScript = redis.NewScript(`
local entry=redis.call('XRANGE',KEYS[1],ARGV[1],ARGV[1],'COUNT',1)
if #entry==0 then return 0 end
local fields=entry[1][2]
local connection=nil
local payload=nil
for i=1,#fields,2 do
 if fields[i]=='connectionId' then connection=fields[i+1] end
 if fields[i]=='payload' then payload=fields[i+1] end
end
if not connection or not payload then return 0 end
redis.call('XADD',KEYS[2],'*','connectionId',connection,'payload',payload,'attempts','0')
redis.call('XDEL',KEYS[1],ARGV[1])
return 1
`)

func (q *Queue) ReplayGroupMetadataTimeouts(ctx context.Context) (int, error) {
	entries, err := q.client.XRevRangeN(ctx, deadLetterStream, "+", "-", 500).Result()
	if err != nil {
		return 0, err
	}
	replayed := 0
	for _, entry := range entries {
		reason := strings.ToLower(valueString(entry.Values["error"]))
		if !strings.Contains(reason, "/group/info") || !(strings.Contains(reason, "deadline exceeded") || strings.Contains(reason, "timeout")) {
			continue
		}
		count, err := replayGroupTimeoutScript.Run(ctx, q.client, []string{deadLetterStream, streamName}, entry.ID).Int()
		if err != nil {
			return replayed, err
		}
		replayed += count
	}
	return replayed, nil
}

func (q *Queue) consume(ctx context.Context, handler func(int, []byte) error) {
	for ctx.Err() == nil {
		if err := q.ensureGroup(ctx); err != nil {
			log.Printf("Redis webhook consumer unavailable: %v", err)
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		message, err := q.next(ctx)
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("Redis webhook read failed: %v", err)
			}
			continue
		}
		q.process(ctx, message, handler)
	}
}

func (q *Queue) ensureGroup(ctx context.Context) error {
	err := q.client.XGroupCreateMkStream(ctx, streamName, consumerGroup, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (q *Queue) next(ctx context.Context) (redis.XMessage, error) {
	messages, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: streamName, Group: consumerGroup, Consumer: q.consumer,
		MinIdle: 30 * time.Second, Start: "0-0", Count: 1,
	}).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return redis.XMessage{}, err
	}
	if len(messages) > 0 {
		return messages[0], nil
	}
	streams, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: consumerGroup, Consumer: q.consumer, Streams: []string{streamName, ">"}, Count: 1, Block: 5 * time.Second,
	}).Result()
	if err != nil {
		return redis.XMessage{}, err
	}
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return redis.XMessage{}, redis.Nil
	}
	return streams[0].Messages[0], nil
}

func (q *Queue) process(ctx context.Context, message redis.XMessage, handler func(int, []byte) error) {
	connectionID, err := strconv.Atoi(valueString(message.Values["connectionId"]))
	payload, decodeErr := decompress([]byte(valueString(message.Values["payload"])))
	if err == nil {
		err = decodeErr
	}
	if err == nil {
		err = handler(connectionID, payload)
	}
	if err == nil {
		q.acknowledge(ctx, message.ID)
		return
	}
	attempts, _ := strconv.Atoi(valueString(message.Values["attempts"]))
	attempts++
	if attempts >= maxAttempts {
		values := cloneValues(message.Values)
		values["error"] = err.Error()
		values["failedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		if addErr := q.client.XAdd(ctx, &redis.XAddArgs{Stream: deadLetterStream, Values: values}).Err(); addErr != nil {
			log.Printf("Redis webhook dead-letter failed: message=%s error=%v", message.ID, addErr)
			return
		}
		q.acknowledge(ctx, message.ID)
		log.Printf("Redis webhook moved to dead letter: message=%s attempts=%d error=%v", message.ID, attempts, err)
		return
	}
	if !wait(ctx, time.Duration(attempts*attempts)*time.Second) {
		return
	}
	values := cloneValues(message.Values)
	values["attempts"] = attempts
	if addErr := q.client.XAdd(ctx, &redis.XAddArgs{Stream: streamName, Values: values}).Err(); addErr != nil {
		log.Printf("Redis webhook retry enqueue failed: message=%s error=%v", message.ID, addErr)
		return
	}
	q.acknowledge(ctx, message.ID)
}

func (q *Queue) acknowledge(ctx context.Context, id string) {
	pipe := q.client.TxPipeline()
	pipe.XAck(ctx, streamName, consumerGroup, id)
	pipe.XDel(ctx, streamName, id)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("Redis webhook acknowledge failed: message=%s error=%v", id, err)
	}
}

func compress(payload []byte) ([]byte, error) {
	var result bytes.Buffer
	writer := gzip.NewWriter(&result)
	if _, err := writer.Write(payload); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func decompress(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func valueString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(value)
	}
}

func cloneValues(values map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
