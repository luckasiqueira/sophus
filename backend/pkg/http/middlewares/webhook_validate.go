package middlewares

import "github.com/kataras/iris/v12"

func ValidateWebhook(ctx iris.Context) {
	///
	ctx.Next()
}
