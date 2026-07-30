package sse

import "testing"

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
