package middlewares

import (
	"zubly/backend/utils/env"

	"github.com/kataras/iris/v12"
)

func IsValidAPIToken(ctx iris.Context) {
	if ctx.GetHeader("apitoken") == env.Backend["WPP_API_GLOBAL_TOKEN"] {
		ctx.Next()
	} else {
		ctx.StopWithStatus(iris.StatusUnauthorized)
	}
}
