package controllers

import (
	"fmt"
	"net/http"
	"net/url"
	"sophus/internal/repo"
	"sophus/internal/repo/instances"
	"sophus/pkg/http/middlewares"
	"sophus/utils/env"
	"sophus/web"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func Instances(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(http.StatusUnauthorized)
		return
	}
	connectionsList, err := repo.GetConnectionListByCompany(agent.CompanyId)
	if err != nil {
		fmt.Println(err)
		ctx.StopWithStatus(http.StatusBadRequest)
		return
	}
	ctx.RenderComponent(web.Instances(connectionsList))
}

func InstancePopup(ctx iris.Context) {
	ctx.RenderComponent(web.InstanceCreatePopup())
}

func NewInstance(ctx iris.Context) {
	if !middlewares.IsValidJWT(ctx) {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	connectionType := ctx.FormValue("connection_type")
	if connectionType != "whatsapp_qrcode" {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "apenas conexões por QR Code estão disponíveis"})
		return
	}
	webhookID := uuid.New()
	publicURL := strings.TrimSpace(env.Backend["APP_DOMAIN"])
	if publicURL == "" {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "APP_DOMAIN não configurada"})
		return
	}
	if !strings.Contains(publicURL, "://") {
		publicURL = "https://" + publicURL
	}
	parsedPublicURL, err := url.Parse(publicURL)
	if err != nil || parsedPublicURL.Host == "" || (parsedPublicURL.Scheme != "http" && parsedPublicURL.Scheme != "https") {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": "APP_DOMAIN inválida"})
		return
	}
	publicURL = strings.TrimRight(parsedPublicURL.Scheme+"://"+parsedPublicURL.Host, "/")
	i := instances.InstanceEVO{
		Name:          strings.TrimSpace(ctx.FormValue("connection_name")),
		Type:          connectionType,
		WebhookURL:    fmt.Sprintf("%s/webhook/%s", publicURL, webhookID.String()),
		InstanceID:    uuid.New(),
		ConnectionKey: uuid.New(),
	}
	i.APIToken = strings.ReplaceAll(i.ConnectionKey.String(), "-", "")
	if i.Name == "" {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "nome da conexão é obrigatório"})
		return
	}
	connectionID, err := repo.CreateConnection(repo.ConnectionEVO{
		Name:          i.Name,
		Status:        "creating",
		CompanyID:     agent.CompanyId,
		CreatedAt:     time.Now(),
		InstanceID:    i.InstanceID,
		Webhook:       webhookID,
		APIToken:      i.APIToken,
		ConnectionKey: i.ConnectionKey,
	})
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	if err := i.Create(); err != nil {
		_ = repo.UpdateConnectionStatus(connectionID, "error")
		ctx.StopWithJSON(iris.StatusBadGateway, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.UpdateConnectionCredentials(connectionID, i.InstanceID, i.ConnectionKey); err != nil {
		_ = repo.UpdateConnectionStatus(connectionID, "error")
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.UpdateConnectionStatus(connectionID, "connecting"); err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	if err := i.Connect(); err != nil {
		_ = repo.UpdateConnectionStatus(connectionID, "error")
		ctx.StopWithJSON(iris.StatusBadGateway, iris.Map{"error": err.Error()})
		return
	}
	ctx.RenderComponent(web.InstanceQRCodePanel(connectionID, i.Name))
}
