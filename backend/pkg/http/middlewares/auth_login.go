package middlewares

import (
	"errors"
	"sophus/backend/internal/repo"
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

func AgentIdentifier(ctx iris.Context) (repo.Agent, error) {
	token := ctx.GetCookie("token")
	if token == "" {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo.Agent{}, errors.New("token is empty")
	}
	jwtToken, err := jwt.Verify(jwt.HS256, secret, []byte(token))
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo.Agent{}, errors.New("token is empty")
	}
	var agent repo.Agent
	jwtToken.Claims(&agent)
	return repo.GetAgentByEmail(agent.Email)
}
