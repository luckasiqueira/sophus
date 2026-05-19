package middlewares

import (
	"zubly/backend/internal/database"

	"github.com/kataras/iris/v12"
)

func IsValidAPIToken(ctx iris.Context) {
	if database.CheckValidToken(ctx.GetHeader("apitoken")) {
		ctx.Next()
	} else {
		ctx.StopWithStatus(iris.StatusUnauthorized)
	}
}
