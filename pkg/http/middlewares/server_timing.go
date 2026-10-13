package middlewares

import (
	"fmt"
	"github.com/kataras/iris/v12"
	"time"
)

func ServerTiming(ctx iris.Context, name string, started time.Time) {
	value := fmt.Sprintf("%s;dur=%.2f", name, float64(time.Since(started).Microseconds())/1000)
	if previous := ctx.ResponseWriter().Header().Get("Server-Timing"); previous != "" {
		value = previous + ", " + value
	}
	ctx.Header("Server-Timing", value)
}
