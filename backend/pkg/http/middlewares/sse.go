package middlewares

import (
	"fmt"
	"sophus/backend/pkg/http/middlewares/sse"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
)

func SSEHandler(ctx iris.Context) {
	//agent, err := AgentIdentifier(ctx)
	url := strings.Trim(ctx.GetReferrer().Path, "/messages/")
	fmt.Println(url)
	ctx.Header("Content-Type", "text/event-stream")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "keep-alive")

	client := sse.Global.Register(url)
	defer sse.Global.UnRegister(url)
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
			done := make(chan error, 1)
			go func() {
				_, err := fmt.Fprintf(ctx.ResponseWriter(), "event: message\ndata: %s\n\n", html)
				if err != nil {
					done <- err
					return
				}
			}()
			flusher.Flush()
			done <- nil

			select {
			case err := <-done:
				if err != nil {
					return
				}
			case <-time.After(time.Second * 10):
				return
			}
		case <-ctx.Request().Context().Done():
			return
		}
	}
}
