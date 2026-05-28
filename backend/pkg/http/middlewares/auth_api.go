package middlewares

import (
	"sophus/backend/internal/repo"

	"github.com/kataras/iris/v12"
)

func AuthAPI(ctx iris.Context) {
	if isValidAPIToken(ctx) {
		ctx.Next()
		return
	}
	ctx.StopWithStatus(iris.StatusUnauthorized)
}

func isValidAPIToken(ctx iris.Context) bool {
	return repo.IsValidAPITokenEVO(ctx.GetHeader("apitoken"))
}
