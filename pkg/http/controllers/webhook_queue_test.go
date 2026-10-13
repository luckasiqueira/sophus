package controllers

import (
	"context"
	"errors"
	"testing"
)

func TestEnqueueMessageWebhookReportsUnavailableQueue(t *testing.T) {
	messageWebhookQueue.Lock()
	originalQueue := messageWebhookQueue.queue
	messageWebhookQueue.queue = nil
	messageWebhookQueue.Unlock()
	t.Cleanup(func() {
		messageWebhookQueue.Lock()
		messageWebhookQueue.queue = originalQueue
		messageWebhookQueue.Unlock()
	})

	err := enqueueMessageWebhook(context.Background(), 1, []byte(`{"event":"message"}`))
	if !errors.Is(err, errMessageWebhookQueueUnavailable) {
		t.Fatalf("enqueueMessageWebhook() error = %v, want queue unavailable", err)
	}
}
