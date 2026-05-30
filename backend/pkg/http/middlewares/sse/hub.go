package sse

import "sync"

type Client struct {
	UserId int
	Ch     chan string
}

type Hub struct {
	mu      sync.RWMutex
	clients map[int]*Client
}

var Global = &Hub{
	clients: make(map[int]*Client),
}

func (h *Hub) Register(userId int) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.clients[userId]; ok {
		close(old.Ch)
	}
	client := &Client{
		UserId: userId,
		Ch:     make(chan string, 10),
	}
	h.clients[userId] = client
	return client
}

func (h *Hub) UnRegister(userId int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, userId)
}

func (h *Hub) Send(userId int, html string) {
	h.mu.RLock()
	client, ok := h.clients[userId]
	h.mu.RUnlock()
	if ok {
		select {
		case client.Ch <- html:
		default:
		}
	}
}
