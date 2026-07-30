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
		// StatusFound (302), never Permanent(301/308): a permanent redirect
		// here gets cached by the browser itself, so once someone hits this
		// while logged out, the browser will keep bouncing them to /login
		// forever afterwards - even after they log in and the cookie is
		// valid again, since it never re-asks the server.
		ctx.Redirect("/login", iris.StatusFound)
		return
	}
	ctx.Next()
}

func IsValidJWT(ctx iris.Context) bool {
	token := ctx.GetCookie("token")
	if token == "" {
		return false
	}
	_, err := jwt.Verify(jwt.HS256, secret, []byte(token))
	if err != nil {
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
		return repo.Agent{}, errors.New("error decoding token")
	}

	var agent repo.Agent
	err = jwtToken.Claims(&agent)
	agent, err = repo.GetAgentByEmail(agent.Email)
	return agent, err
}
