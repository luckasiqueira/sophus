package sse

import (
	"testing"
	"time"
)

func TestRetainedHubReplaysLatestEvent(t *testing.T) {
	hub := NewHub(true)
	hub.SendEvent("connection-1", "qrcode", `{"count":1}`)

	client := hub.Register("connection-1")
	defer hub.Unregister(client)
	event := <-client.Ch
	if event.Name != "qrcode" || event.Data != `{"count":1}` {
		t.Fatalf("unexpected replayed event: %#v", event)
	}
}

func TestHubPublishesToEverySubscriber(t *testing.T) {
	hub := NewHub(false)
	first := hub.Register("connection-1")
	second := hub.Register("connection-1")
	defer hub.Unregister(first)
	defer hub.Unregister(second)

	hub.SendEvent("connection-1", "connection", `{"status":"connected"}`)
	for _, client := range []*Client{first, second} {
		event := <-client.Ch
		if event.Name != "connection" {
			t.Fatalf("unexpected event for subscriber: %#v", event)
		}
	}
}

func TestNotifyConversationsPublishesCompanyRefresh(t *testing.T) {
	original := Conversations
	Conversations = NewHub(false)
	defer func() { Conversations = original }()

	client := Conversations.Register("42")
	defer Conversations.Unregister(client)
	NotifyConversations(42)

	select {
	case event := <-client.Ch:
		if event.Name != "conversation" || event.Data != "refresh" {
			t.Fatalf("unexpected conversation event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("conversation refresh event was not published")
	}
}

func TestNotifyInstancesPublishesCompanyRefresh(t *testing.T) {
	original := Instances
	Instances = NewHub(false)
	defer func() { Instances = original }()

	client := Instances.Register("42")
	defer Instances.Unregister(client)
	NotifyInstances(42)

	select {
	case event := <-client.Ch:
		if event.Name != "instance-status" || event.Data != "refresh" {
			t.Fatalf("unexpected instance event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("instance refresh event was not published")
	}
}
