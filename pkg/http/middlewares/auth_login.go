package middlewares

import (
	"errors"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
)

var secret = []byte(strings.TrimSpace(env.Backend["SALT_JWT"]))

const agentContextKey = "authenticatedAgent"

func AuthUser(ctx iris.Context) {
	started := time.Now()
	if HasSupportCookie(ctx) && !supportExitRequest(ctx) {
		agent, err := identifySupportAgent(ctx)
		if err != nil {
			supportAuthenticationFailure(ctx)
			return
		}
		ctx.Values().Set(agentContextKey, agent)
		ServerTiming(ctx, "auth", started)
		ctx.Next()
		return
	}
	if admin, err := identifyPlatformAdmin(ctx); err == nil {
		ctx.Values().Set(platformAdminContextKey, admin)
		ServerTiming(ctx, "auth", started)
		ctx.Next()
		return
	}
	if agent, err := identifyAgent(ctx); err == nil {
		ctx.Values().Set(agentContextKey, agent)
		ServerTiming(ctx, "auth", started)
		ctx.Next()
		return
	}
	if strings.HasPrefix(ctx.Path(), "/management/api/") {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	ctx.Redirect("/login", iris.StatusFound)
}

func AuthLogin(ctx iris.Context) {
	started := time.Now()
	agent, err := identifyAgent(ctx)
	if err != nil {
		if HasSupportCookie(ctx) {
			supportAuthenticationFailure(ctx)
			return
		}
		// StatusFound (302), never Permanent(301/308): a permanent redirect
		// here gets cached by the browser itself, so once someone hits this
		// while logged out, the browser will keep bouncing them to /login
		// forever afterwards - even after they log in and the cookie is
		// valid again, since it never re-asks the server.
		ctx.Redirect("/login", iris.StatusFound)
		return
	}
	ctx.Values().Set(agentContextKey, agent)
	ServerTiming(ctx, "auth", started)
	ctx.Next()
}

func IsValidJWT(ctx iris.Context) bool {
	if HasSupportCookie(ctx) {
		_, err := identifySupportAgent(ctx)
		return err == nil
	}
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

func OptionalAgent(ctx iris.Context) (repo.Agent, bool) {
	if agent, ok := ctx.Values().Get(agentContextKey).(repo.Agent); ok {
		return agent, true
	}
	agent, err := identifyAgent(ctx)
	if err != nil {
		return repo.Agent{}, false
	}
	ctx.Values().Set(agentContextKey, agent)
	return agent, true
}

func identifyAgent(ctx iris.Context) (repo.Agent, error) {
	if HasSupportCookie(ctx) {
		return identifySupportAgent(ctx)
	}
	if len(secret) == 0 {
		return repo.Agent{}, errors.New("JWT secret is empty")
	}
	token := ctx.GetCookie("token")
	if token == "" {
		return repo.Agent{}, errors.New("token is empty")
	}
	jwtToken, err := jwt.Verify(jwt.HS256, secret, []byte(token))
	if err != nil {
		return repo.Agent{}, errors.New("error decoding token")
	}

	var claims struct {
		Id      int `json:"id"`
		Version int `json:"version"`
	}
	if err := jwtToken.Claims(&claims); err != nil {
		return repo.Agent{}, err
	}
	if claims.Id <= 0 {
		return repo.Agent{}, errors.New("invalid agent claims")
	}
	session, err := repo.GetSessionAgent(claims.Id)
	if err != nil {
		return repo.Agent{}, err
	}
	agent := session.Agent
	if claims.Version != agent.SessionVersion {
		return repo.Agent{}, errors.New("agent session is no longer valid")
	}
	if !agent.IsActive {
		return repo.Agent{}, errors.New("agent is inactive")
	}
	if !session.CompanyActive {
		return repo.Agent{}, errors.New("company is inactive")
	}
	return agent, nil
}
