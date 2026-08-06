package controllers

import (
	"fmt"
	"log"
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
		log.Printf("failed to persist new connection %q: %v", i.Name, err)
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	log.Printf("creating Evolution GO instance: connection=%d name=%q webhook=%s", connectionID, i.Name, i.WebhookURL)
	if err := i.Create(); err != nil {
		log.Printf("failed to create Evolution GO instance for connection %d: %v", connectionID, err)
		_ = repo.UpdateConnectionStatus(connectionID, "error")
		ctx.StopWithJSON(iris.StatusBadGateway, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.UpdateConnectionCredentials(connectionID, i.InstanceID, i.ConnectionKey); err != nil {
		log.Printf("failed to update Evolution GO credentials for connection %d: %v", connectionID, err)
		_ = repo.UpdateConnectionStatus(connectionID, "error")
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	if err := repo.UpdateConnectionStatus(connectionID, "connecting"); err != nil {
		log.Printf("failed to mark connection %d as connecting: %v", connectionID, err)
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	if err := i.Connect(); err != nil {
		log.Printf("failed to configure Evolution GO webhook for connection %d: %v", connectionID, err)
		_ = repo.UpdateConnectionStatus(connectionID, "error")
		ctx.StopWithJSON(iris.StatusBadGateway, iris.Map{"error": err.Error()})
		return
	}
	log.Printf("Evolution GO instance connected to webhook configuration: connection=%d", connectionID)
	go awaitInitialQRCode(connectionID, i)
	ctx.RenderComponent(web.InstanceQRCodePanel(connectionID, i.Name))
}

func awaitInitialQRCode(connectionID int, instance instances.InstanceEVO) {
	// Connect starts the Evolution GO client asynchronously. Give it time to
	// register the client before /instance/qr attempts to start another one.
	time.Sleep(2 * time.Second)

	publishedCount := 0
	for attempt := 1; attempt <= 90; attempt++ {
		connection, err := repo.GetConnectionById(connectionID)
		if err == nil && connection.Status == "connected" {
			return
		}
		if err == nil && (connection.Status == "disconnected" || connection.Status == "error") {
			return
		}
		qrcode, err := instance.GetQRCode()
		if err == nil && qrcode.QRCode != connection.QRCode {
			publishedCount++
			if qrcode.Count == 0 {
				qrcode.Count = publishedCount
			}
			published, publishErr := publishQRCode(connectionID, qrcode.Code, qrcode.QRCode, qrcode.Count, qrcode.MaxCount)
			if publishErr != nil {
				log.Printf("failed to publish polled QR Code for connection %d: %v", connectionID, publishErr)
				return
			}
			if published {
				log.Printf("QR Code obtained from Evolution GO fallback: connection=%d attempt=%d", connectionID, attempt)
			}
		}
		if attempt == 1 || attempt%5 == 0 {
			log.Printf("waiting for Evolution GO QR Code: connection=%d attempt=%d error=%v", connectionID, attempt, err)
		}
		time.Sleep(2 * time.Second)
	}
	log.Printf("Evolution GO QR polling ended without a connected session: connection=%d", connectionID)
}
