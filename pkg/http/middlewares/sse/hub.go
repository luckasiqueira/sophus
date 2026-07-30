package sse

import "sync"

type Client struct {
	Key string
	Ch  chan Event
}

type Event struct {
	Name string
	Data string
}

type Hub struct {
	mu       sync.RWMutex
	clients  map[string]map[*Client]struct{}
	last     map[string]Event
	retained bool
}

func NewHub(retained bool) *Hub {
	return &Hub{
		clients:  make(map[string]map[*Client]struct{}),
		last:     make(map[string]Event),
		retained: retained,
	}
}

var Global = NewHub(false)
var QRGlobal = NewHub(true)

func (h *Hub) Register(key string) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	client := &Client{Key: key, Ch: make(chan Event, 32)}
	if h.clients[key] == nil {
		h.clients[key] = make(map[*Client]struct{})
	}
	h.clients[key][client] = struct{}{}
	if last, ok := h.last[key]; ok {
		client.Ch <- last
	}
	return client
}

func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	clients := h.clients[client.Key]
	delete(clients, client)
	if len(clients) == 0 {
		delete(h.clients, client.Key)
	}
}

func (h *Hub) Send(key, data string) {
	h.SendEvent(key, "", data)
}

func (h *Hub) SendEvent(key, event, data string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	payload := Event{Name: event, Data: data}
	if h.retained {
		h.last[key] = payload
	}
	for client := range h.clients[key] {
		select {
		case client.Ch <- payload:
		default:
		}
	}
}

func (h *Hub) Clear(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.last, key)
}
