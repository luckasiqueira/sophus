package webhookqueue

import (
	"bytes"
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"testing"
)

func TestPayloadCompressionRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte(`{"data":{"message":{"base64":"abcdef0123456789"}}}`), 100)
	compressed, err := compress(payload)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if len(compressed) >= len(payload) {
		t.Fatalf("compressed payload did not shrink: got %d, original %d", len(compressed), len(payload))
	}
	decoded, err := decompress(compressed)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("decompressed payload differs from original")
	}
}

func TestGroupTimeoutDeadLettersReplayOnceAndPreserveOtherFailures(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	queue := &Queue{client: client}
	ctx := context.Background()
	payload, err := compress([]byte(`{"data":{"Info":{"IsGroup":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{`Post "https://api.test/group/info": context deadline exceeded`, `storage limit reached`} {
		if err := client.XAdd(ctx, &redis.XAddArgs{Stream: deadLetterStream, Values: map[string]any{"connectionId": 9, "payload": payload, "attempts": 5, "error": reason}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	count, err := queue.ReplayGroupMetadataTimeouts(ctx)
	if err != nil || count != 1 {
		t.Fatalf("replayed=%d error=%v", count, err)
	}
	count, err = queue.ReplayGroupMetadataTimeouts(ctx)
	if err != nil || count != 0 {
		t.Fatal("recovery duplicated the webhook")
	}
	entries, err := client.XRange(ctx, streamName, "-", "+").Result()
	if err != nil || len(entries) != 1 {
		t.Fatal("recovered webhook missing")
	}
	if valueString(entries[0].Values["attempts"]) != "0" {
		t.Fatal("retry attempts not reset")
	}
	if remaining, err := client.XLen(ctx, deadLetterStream).Result(); err != nil || remaining != 1 {
		t.Fatal("unrelated dead letter was removed")
	}
}

func TestDecompressRejectsInvalidPayload(t *testing.T) {
	if _, err := decompress([]byte("not-gzip")); err == nil {
		t.Fatal("expected invalid gzip payload to fail")
	}
}
