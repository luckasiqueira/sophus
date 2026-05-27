package middlewares

import (
	"sophus/backend/internal/repo"
	"sophus/backend/utils/env"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/jwt"
	"golang.org/x/crypto/bcrypt"
)

var secret = []byte(env.Backend["SALT_JWT"])

func DoLogin(ctx iris.Context) {
	agent, status, err := checkCredentials(ctx)
	if err != nil {
		ctx.StopWithStatus(status)
	}
	type AgentJWT struct {
		Name  string
		Email string
		Role  string
	}
	signer := jwt.NewSigner(jwt.HS256, secret, (7*24)*time.Hour)
	token, err := signer.Sign(AgentJWT{
		Name:  agent.Name,
		Email: agent.Email,
		Role:  agent.Role,
	})
	if err != nil {
		//ctx.StopWithStatus(iris.StatusUnauthorized)
		ctx.JSON(iris.Map{"message": "Login inválido"})
	}
	ctx.JSON(iris.Map{"token": string(token)})

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
