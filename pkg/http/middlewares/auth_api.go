package middlewares

import (
	"sophus/internal/repo"

	"github.com/kataras/iris/v12"
)

const authMethodContextKey = "authenticationMethod"

const (
	authMethodAPI    = "api"
	authMethodCookie = "cookie"
)

func AuthAPI(ctx iris.Context) {
	if isValidAPIToken(ctx) {
		ctx.Values().Set(authMethodContextKey, authMethodAPI)
		ctx.Next()
		return
	}
	agent, err := identifyAgent(ctx)
	if err == nil {
		ctx.Values().Set(agentContextKey, agent)
		ctx.Values().Set(authMethodContextKey, authMethodCookie)
		ctx.Next()
		return
	}
	ctx.StopWithStatus(iris.StatusUnauthorized)
}

func isValidAPIToken(ctx iris.Context) bool {
	token := ctx.GetHeader("apitoken")
	return token != "" && repo.IsValidAPITokenEVO(token)
}

func IsAPIAuthentication(ctx iris.Context) bool {
	return ctx.Values().GetString(authMethodContextKey) == authMethodAPI
}
