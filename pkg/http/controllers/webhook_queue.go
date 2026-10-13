package controllers

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"sophus/internal/repo"
	"sophus/internal/webhookqueue"
)

var messageWebhookQueue struct {
	sync.RWMutex
	queue *webhookqueue.Queue
}

var errMessageWebhookQueueUnavailable = errors.New("message webhook queue unavailable")

func StartWebhookQueue(ctx context.Context) error {
	err := initializeWebhookQueue(ctx)
	if err != nil {
		go retryWebhookQueue(ctx)
	}
	return err
}

func initializeWebhookQueue(ctx context.Context) error {
	messageWebhookQueue.RLock()
	started := messageWebhookQueue.queue != nil
	messageWebhookQueue.RUnlock()
	if started {
		return nil
	}
	queue, err := webhookqueue.New()
	if err != nil {
		return err
	}
	pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := queue.Ping(pingContext); err != nil {
		_ = queue.Close()
		return err
	}
	messageWebhookQueue.Lock()
	if messageWebhookQueue.queue != nil {
		messageWebhookQueue.Unlock()
		_ = queue.Close()
		return nil
	}
	messageWebhookQueue.queue = queue
	messageWebhookQueue.Unlock()
	queue.Start(ctx, func(connectionID int, payload []byte) error {
		connection, err := repo.GetConnectionById(connectionID)
		if err != nil {
			return err
		}
		err = processMessageWebhook(connection, payload)
		if errors.Is(err, repo.ErrMessageWithoutIdentity) {
			log.Printf("message webhook discarded: connection=%d error=%v", connectionID, err)
			return nil
		}
		return err
	})
	return nil
}

func retryWebhookQueue(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := initializeWebhookQueue(ctx); err != nil {
				log.Printf("webhook queue reconnect failed: %v", err)
				continue
			}
			log.Printf("webhook queue connected after startup failure")
			return
		case <-ctx.Done():
			return
		}
	}
}

func enqueueMessageWebhook(ctx context.Context, connectionID int, payload []byte) error {
	messageWebhookQueue.RLock()
	queue := messageWebhookQueue.queue
	messageWebhookQueue.RUnlock()
	if queue == nil {
		return errMessageWebhookQueueUnavailable
	}
	return queue.Enqueue(ctx, connectionID, payload)
}
