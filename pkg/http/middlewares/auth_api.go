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
		allowed, accessErr := repo.CompanyHasAccess(agent.CompanyId)
		if accessErr != nil || !allowed {
			ctx.StopWithStatus(iris.StatusPaymentRequired)
			return
		}
		ctx.Values().Set(agentContextKey, agent)
		ctx.Values().Set(authMethodContextKey, authMethodCookie)
		ctx.Next()
		return
	}
	ctx.StopWithStatus(iris.StatusUnauthorized)
}

func isValidAPIToken(ctx iris.Context) bool {
	token := ctx.GetHeader("apitoken")
	if token == "" {
		return false
	}
	connection, err := repo.GetConnectionByAPI(token)
	if err != nil || connection.Status != repo.ConnectionStatusConnected {
		return false
	}
	allowed, err := repo.CompanyHasAccess(connection.CompanyID)
	return err == nil && allowed
}

func IsAPIAuthentication(ctx iris.Context) bool {
	return ctx.Values().GetString(authMethodContextKey) == authMethodAPI
}
