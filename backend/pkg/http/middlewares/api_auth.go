package middlewares

import (
	"zubly/backend/internal/repo"

	"github.com/kataras/iris/v12"
)

func IsValidAPIToken(ctx iris.Context) {
	if repo.IsValidAPIToken(ctx.GetHeader("apitoken")) {
		ctx.Next()
	} else {
		ctx.StopWithStatus(iris.StatusUnauthorized)
	}
}
