package middlewares

import (
	"sophus/backend/utils/env"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
)

var secret = []byte(env.Backend["SALT_JWT"])

func AuthLogin(ctx iris.Context) {
	token := ctx.GetCookie("token")
	if token == "" {
		ctx.Redirect("/login", iris.StatusPermanentRedirect)
		return
	}

	_, err := jwt.Verify(jwt.HS256, secret, []byte(token))
	if err != nil {
		ctx.Redirect("/login", iris.StatusPermanentRedirect)
	}
	ctx.Next()
}
