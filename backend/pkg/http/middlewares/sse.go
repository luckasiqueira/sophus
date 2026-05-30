package middlewares

import (
	"fmt"
	"sophus/backend/pkg/http/middlewares/sse"

	"github.com/kataras/iris/v12"
)

func SSEHandler(ctx iris.Context) {
	agent, err := AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	ctx.Header("Content-Type", "text/event-stream")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "keep-alive")

	client := sse.Global.Register(agent.Id)
	defer sse.Global.UnRegister(agent.Id)
	flusher, ok := ctx.ResponseWriter().Flusher()
	if !ok {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	fmt.Fprintf(ctx.ResponseWriter(), "event: connected\ndata: ok\n\n")
	flusher.Flush()
	for {
		select {
		case html, open := <-client.Ch:
			if !open {
				return
			}
			fmt.Fprintf(ctx.ResponseWriter(), "event: message\ndata: %s\n\n", html)
			flusher.Flush()
		case <-ctx.Request().Context().Done():
			return
		}
	}
}
