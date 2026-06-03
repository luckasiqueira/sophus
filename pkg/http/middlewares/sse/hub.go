package sse

import "sync"

type Client struct {
	URL string
	Ch  chan string
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

var Global = &Hub{
	clients: make(map[string]*Client),
}

func (h *Hub) Register(url string) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.clients[url]; ok {
		close(old.Ch)
	}
	client := &Client{
		URL: url,
		Ch:  make(chan string, 10),
	}
	h.clients[url] = client
	return client
}

func (h *Hub) UnRegister(url string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, url)
}

func (h *Hub) Send(url, html string) {
	h.mu.RLock()
	client, ok := h.clients[url]
	h.mu.RUnlock()
	if ok {
		select {
		case client.Ch <- html:
		default:
		}
	}
}
