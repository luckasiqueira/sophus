package middlewares

import (
	"sophus/backend/internal/repo"

	"github.com/kataras/iris/v12"
)

func Auth(ctx iris.Context) {
	if isValidAPIToken(ctx) {
		ctx.Next()
	}

}

func isValidAPIToken(ctx iris.Context) bool {
	return repo.IsValidAPITokenEVO(ctx.GetHeader("apitoken"))
}

func isValidJWT(ctx iris.Context) bool {
	return true
}
