package middlewares

import (
	"sophus/internal/media"

	"github.com/kataras/iris/v12"
)

func AuthFlowMedia(ctx iris.Context) {
	companyID, err := ctx.Params().GetInt("companyId")
	if err != nil || !media.Verify(companyID, ctx.Params().Get("file"), ctx.URLParam("token")) {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	ctx.Next()
}
