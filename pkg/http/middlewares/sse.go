package middlewares

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/middlewares/sse"

	"github.com/kataras/iris/v12"
)

func SSEMessages(ctx iris.Context) {
	key := strings.TrimPrefix(ctx.GetReferrer().Path, "/messages/")
	streamSSE(ctx, sse.Global, key, "message")
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
	streamSSE(ctx, sse.QRGlobal, strconv.Itoa(connectionID), "qrcode")
}

func streamSSE(ctx iris.Context, hub *sse.Hub, key, event string) {
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
			eventName := incoming.Name
			if eventName == "" {
				eventName = event
			}
			if err := writeSSE(ctx, flusher, eventName, incoming.Data); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(ctx.ResponseWriter(), ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ctx.Request().Context().Done():
			return
		}
	}
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
