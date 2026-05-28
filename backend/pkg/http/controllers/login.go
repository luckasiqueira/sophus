package controllers

import (
	"fmt"
	"sophus/backend/internal/repo"
	"sophus/backend/utils/env"
	"sophus/backend/web"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
	"golang.org/x/crypto/bcrypt"
)

func Login(ctx iris.Context) {
	ctx.RenderComponent(web.Login())
}

func DoLogin(ctx iris.Context) {
	agent, status, err := checkCredentials(ctx)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(status)
		return
	}
	type AgentJWT struct {
		Name  string
		Email string
		Role  string
	}
	signer := jwt.NewSigner(jwt.HS256, []byte(env.Backend["SALT_JWT"]), (7*24)*time.Hour)
	token, err := signer.Sign(AgentJWT{
		Name:  agent.Name,
		Email: agent.Email,
		Role:  agent.Role,
	})
	if err != nil {
		ctx.JSON(iris.Map{"message": "Login inválido"})
	}

	ctx.SetCookieKV("token", string(token))
	ctx.Header("HX-Redirect", "/")
}

func checkCredentials(ctx iris.Context) (repo.Agent, int, error) {
	givenEmail := ctx.FormValue("email")
	givenPassword := ctx.FormValue("password")
	agent, err := repo.GetAgentByEmail(givenEmail)
	if err != nil {
		return repo.Agent{}, iris.StatusBadRequest, err
	}
	err = bcrypt.CompareHashAndPassword([]byte(agent.Password), []byte(givenPassword))
	if err != nil {
		return repo.Agent{}, iris.StatusUnauthorized, err
	}
	return agent, iris.StatusOK, nil
}
