package middlewares

import (
	"errors"
	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
)

var secret = []byte(env.Backend["SALT_JWT"])

func AuthLogin(ctx iris.Context) {
	if !IsValidJWT(ctx) {
		ctx.Redirect("/login", iris.StatusPermanentRedirect)
		return
	}
	ctx.Next()
}

func IsValidJWT(ctx iris.Context) bool {
	token := ctx.GetCookie("token")
	if token == "" {
		//ctx.Redirect("/login", iris.StatusPermanentRedirect)
		return false
	}
	_, err := jwt.Verify(jwt.HS256, secret, []byte(token))
	if err != nil {
		//ctx.Redirect("/login", iris.StatusPermanentRedirect)
		return false
	}
	return true
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
	err = jwtToken.Claims(&agent)
	agent, err = repo.GetAgentByEmail(agent.Email)
	return agent, err //repo.GetAgentByEmail(agent.Email)
}
