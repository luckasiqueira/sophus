package middlewares

import (
	"errors"

	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
)

var secret = []byte(env.Backend["SALT_JWT"])

const agentContextKey = "authenticatedAgent"

func AuthLogin(ctx iris.Context) {
	agent, err := identifyAgent(ctx)
	if err != nil {
		// StatusFound (302), never Permanent(301/308): a permanent redirect
		// here gets cached by the browser itself, so once someone hits this
		// while logged out, the browser will keep bouncing them to /login
		// forever afterwards - even after they log in and the cookie is
		// valid again, since it never re-asks the server.
		ctx.Redirect("/login", iris.StatusFound)
		return
	}
	ctx.Values().Set(agentContextKey, agent)
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
	if agent, ok := ctx.Values().Get(agentContextKey).(repo.Agent); ok {
		return agent, nil
	}

	agent, err := identifyAgent(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return repo.Agent{}, err
	}
	ctx.Values().Set(agentContextKey, agent)
	return agent, nil
}

func identifyAgent(ctx iris.Context) (repo.Agent, error) {
	token := ctx.GetCookie("token")
	if token == "" {
		return repo.Agent{}, errors.New("token is empty")
	}
	jwtToken, err := jwt.Verify(jwt.HS256, secret, []byte(token))
	if err != nil {
		return repo.Agent{}, errors.New("error decoding token")
	}

	var agent repo.Agent
	if err := jwtToken.Claims(&agent); err != nil {
		return repo.Agent{}, err
	}
	agent, err = repo.GetAgentByEmail(agent.Email)
	if err != nil {
		return repo.Agent{}, err
	}
	if !agent.IsActive {
		return repo.Agent{}, errors.New("agent is inactive")
	}
	return agent, nil
}
