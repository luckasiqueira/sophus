package middlewares

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

const maxSSEConnectionsPerAgent = 10

var sseConnectionCounts = struct {
	sync.Mutex
	byAgent map[int]int
}{byAgent: map[int]int{}}

func SSEMessages(ctx iris.Context) {
	key := strings.TrimSpace(ctx.URLParam("conversation"))
	if key == "" {
		key = strings.TrimPrefix(ctx.GetReferrer().Path, "/messages/")
	}
	conversationURL, err := uuid.Parse(key)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	agent, err := AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	if _, err := repo.GetVisibleConversationByURL(conversationURL, agent); err != nil {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	streamSSE(ctx, sse.Global, key, "message", agent.Id)
}

func SSEConversations(ctx iris.Context) {
	agent, err := AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	streamSSE(ctx, sse.Conversations, strconv.Itoa(agent.CompanyId), "conversation", agent.Id)
}

func SSEInstances(ctx iris.Context) {
	agent, err := AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	streamSSE(ctx, sse.Instances, strconv.Itoa(agent.CompanyId), "instance-status", agent.Id)
}

func SSEQRCode(ctx iris.Context) {
	agent, err := AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	connectionID, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	connection, err := repo.GetConnectionById(connectionID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	if connection.CompanyID != agent.CompanyId {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	streamSSE(ctx, sse.QRGlobal, strconv.Itoa(connectionID), "qrcode", agent.Id)
}

func streamSSE(ctx iris.Context, hub *sse.Hub, key, event string, agentID int) {
	supportSession, support := SupportSession(ctx)
	if support {
		agentID = -supportSession.Admin.Id
	}
	if !acquireSSEConnection(agentID) {
		ctx.StopWithStatus(iris.StatusTooManyRequests)
		return
	}
	defer releaseSSEConnection(agentID)
	ctx.Header("Content-Type", "text/event-stream")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "keep-alive")
	ctx.Header("X-Accel-Buffering", "no")

	client := hub.Register(key)
	log.Printf("SSE stream opened: event=%s key=%s", event, key)
	defer func() {
		hub.Unregister(client)
		log.Printf("SSE stream closed: event=%s key=%s", event, key)
	}()
	flusher, ok := ctx.ResponseWriter().Flusher()
	if !ok {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	writeSSE(ctx, flusher, "ready", "ok")
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case incoming := <-client.Ch:
			if support {
				if _, err := identifySupportAgent(ctx); err != nil {
					return
				}
			}
			eventName := incoming.Name
			if eventName == "" {
				eventName = event
			}
			if err := writeSSE(ctx, flusher, eventName, incoming.Data); err != nil {
				return
			}
		case <-heartbeat.C:
			if support {
				if _, err := identifySupportAgent(ctx); err != nil {
					return
				}
			}
			if _, err := fmt.Fprint(ctx.ResponseWriter(), ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ctx.Request().Context().Done():
			return
		}
	}
}

func acquireSSEConnection(agentID int) bool {
	sseConnectionCounts.Lock()
	defer sseConnectionCounts.Unlock()
	if sseConnectionCounts.byAgent[agentID] >= maxSSEConnectionsPerAgent {
		return false
	}
	sseConnectionCounts.byAgent[agentID]++
	return true
}

func releaseSSEConnection(agentID int) {
	sseConnectionCounts.Lock()
	defer sseConnectionCounts.Unlock()
	if sseConnectionCounts.byAgent[agentID] <= 1 {
		delete(sseConnectionCounts.byAgent, agentID)
		return
	}
	sseConnectionCounts.byAgent[agentID]--
}

func writeSSE(ctx iris.Context, flusher http.Flusher, event, data string) error {
	if _, err := fmt.Fprintf(ctx.ResponseWriter(), "event: %s\n", event); err != nil {
		return err
	}
	for _, line := range strings.Split(data, "\n") {
		if _, err := fmt.Fprintf(ctx.ResponseWriter(), "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(ctx.ResponseWriter(), "\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
