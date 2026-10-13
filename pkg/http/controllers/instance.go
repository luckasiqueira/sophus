package controllers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sophus/internal/instancesync"
	"sophus/internal/repo"
	"sophus/internal/repo/instances"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/utils/env"
	"sophus/web"

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
	go instancesync.SyncCompany(agent.CompanyId)
	ctx.RenderComponent(web.Instances(connectionsList))
}

func InstanceList(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	connections, err := repo.GetConnectionListByCompany(agent.CompanyId)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.RenderComponent(web.InstanceList(connections))
}

func InstancePopup(ctx iris.Context) {
	ctx.RenderComponent(web.InstanceCreatePopup())
}

func ImportInstanceContacts(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		return
	}
	connectionID, err := ctx.Params().GetInt("id")
	if err != nil || connectionID <= 0 {
		writeJSONError(ctx, iris.StatusBadRequest, "instância inválida")
		return
	}
	connection, err := repo.GetConnectionById(connectionID)
	if err != nil {
		log.Printf("contact import failed loading connection: connection=%d company=%d error=%v", connectionID, agent.CompanyId, err)
		writeJSONError(ctx, iris.StatusNotFound, "instância não encontrada")
		return
	}
	if connection.CompanyID != agent.CompanyId {
		writeJSONError(ctx, iris.StatusNotFound, "instância não encontrada")
		return
	}
	if connection.Status != repo.ConnectionStatusConnected {
		writeJSONError(ctx, iris.StatusConflict, "a instância precisa estar conectada para importar contatos")
		return
	}
	upstream := instances.InstanceEVO{APIToken: connection.EvolutionAPIKey()}
	contacts, err := upstream.GetContacts()
	if err != nil {
		log.Printf("contact import failed upstream: connection=%d company=%d error=%v", connectionID, agent.CompanyId, err)
		writeJSONError(ctx, iris.StatusBadGateway, "não foi possível obter os contatos do WhatsApp")
		return
	}
	const maxImportedContacts = 20000
	overLimit := 0
	if len(contacts) > maxImportedContacts {
		overLimit = len(contacts) - maxImportedContacts
		contacts = contacts[:maxImportedContacts]
	}
	unique := make(map[string]repo.ContactImport, len(contacts))
	invalid := overLimit
	duplicates := 0
	excluded := 0
	for _, contact := range contacts {
		item, valid := repo.NormalizeImportedContact(contact.PreferredName(), contact.BusinessName, contact.JID)
		if !valid {
			invalid++
			continue
		}
		if !contact.IsAddressBookContact() {
			excluded++
			continue
		}
		if _, duplicated := unique[item.Number]; duplicated {
			duplicates++
			continue
		}
		unique[item.Number] = item
	}
	items := make([]repo.ContactImport, 0, len(unique))
	for _, contact := range unique {
		items = append(items, contact)
	}
	result, err := repo.ImportConnectionContacts(agent.CompanyId, connectionID, items, invalid)
	if err != nil {
		log.Printf("contact import failed saving contacts: connection=%d company=%d received=%d valid=%d invalid=%d error=%v", connectionID, agent.CompanyId, len(contacts)+overLimit, len(items), invalid, err)
		writeJSONError(ctx, iris.StatusInternalServerError, "não foi possível importar os contatos")
		return
	}
	result.Received = len(contacts) + overLimit
	result.Skipped += duplicates
	result.Excluded = excluded
	ctx.JSON(iris.Map{"data": result})
}

func RenameInstance(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	id, err := ctx.Params().GetInt("id")
	name := strings.TrimSpace(ctx.FormValue("name"))
	if err != nil || id <= 0 || name == "" || len(name) > 120 {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "informe um nome de até 120 caracteres"})
		return
	}
	updated, err := repo.RenameConnection(agent.CompanyId, id, name)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if !updated {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	sse.NotifyInstances(agent.CompanyId)
	ctx.StatusCode(iris.StatusNoContent)
}

func DeleteInstance(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	id, err := ctx.Params().GetInt("id")
	if err != nil || id <= 0 {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	connection, err := repo.GetConnectionByID(agent.CompanyId, id)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	history := ctx.URLParamDefault("history", "keep")
	if history != "keep" && history != "delete" {
		ctx.StopWithJSON(iris.StatusBadRequest, iris.Map{"error": "escolha como tratar o histórico da conexão"})
		return
	}
	remote := instances.InstanceEVO{InstanceID: connection.InstanceID}
	if err := remote.Delete(); err != nil {
		log.Printf("Evolution GO instance deletion failed: connection=%d instance=%s error=%v", connection.Id, connection.InstanceID, err)
		ctx.StopWithJSON(iris.StatusBadGateway, iris.Map{"error": "não foi possível remover a instância no WhatsApp"})
		return
	}
	deleted, err := repo.DeleteConnection(agent.CompanyId, id, history == "delete")
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	if !deleted {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	sse.NotifyInstances(agent.CompanyId)
	sse.NotifyConversations(agent.CompanyId)
	ctx.StatusCode(iris.StatusNoContent)
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
	publicURL, err := applicationPublicURL()
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
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
		if errors.Is(err, repo.ErrConnectionLimitReached) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": "o limite de conexões do plano foi atingido"})
			return
		}
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
	recordCompanyAction(ctx, agent, "create", "instance", strconv.Itoa(connectionID))
	go awaitInitialQRCode(connectionID, i)
	ctx.RenderComponent(web.InstanceQRCodePanel(connectionID, i.Name))
}

func ReconnectInstance(ctx iris.Context) {
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusUnauthorized)
		return
	}
	connectionID, err := ctx.Params().GetInt("id")
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	connection, err := repo.GetConnectionById(connectionID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusNotFound)
		return
	}
	if connection.CompanyID != agent.CompanyId {
		ctx.StopWithStatus(iris.StatusForbidden)
		return
	}
	publicURL, err := applicationPublicURL()
	if err != nil {
		ctx.StopWithJSON(iris.StatusInternalServerError, iris.Map{"error": err.Error()})
		return
	}
	instance := instances.InstanceEVO{
		Name:          connection.Name,
		WebhookURL:    fmt.Sprintf("%s/webhook/%s", publicURL, connection.Webhook.String()),
		InstanceID:    connection.InstanceID,
		ConnectionKey: connection.ConnectionKey,
		APIToken:      connection.EvolutionAPIKey(),
	}
	var startErr error
	if err := instance.ReconnectWithStart(func() error {
		startErr = repo.UpdateConnectionStatus(connectionID, repo.ConnectionStatusConnecting)
		if startErr == nil {
			sse.NotifyInstances(agent.CompanyId)
		}
		return startErr
	}); err != nil {
		if errors.Is(err, instances.ErrReconnectInProgress) {
			ctx.StopWithJSON(iris.StatusConflict, iris.Map{"error": err.Error()})
			return
		}
		if startErr != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		log.Printf("manual Evolution GO reconnect failed: connection=%d error=%v", connectionID, err)
		_ = repo.UpdateConnectionStatus(connectionID, repo.ConnectionStatusDisconnected)
		sse.NotifyInstances(agent.CompanyId)
		ctx.StopWithJSON(iris.StatusBadGateway, iris.Map{"error": "não foi possível restabelecer a conexão no WhatsApp; tente novamente em alguns instantes"})
		return
	}
	go awaitInitialQRCode(connectionID, instance)
	ctx.RenderComponent(web.InstanceReconnectPopup(connectionID, connection.Name))
}

func applicationPublicURL() (string, error) {
	publicURL := strings.TrimSpace(env.Backend["APP_DOMAIN"])
	if publicURL == "" {
		return "", fmt.Errorf("APP_DOMAIN não configurada")
	}
	if !strings.Contains(publicURL, "://") {
		publicURL = "https://" + publicURL
	}
	parsedPublicURL, err := url.Parse(publicURL)
	if err != nil || parsedPublicURL.Host == "" || (parsedPublicURL.Scheme != "http" && parsedPublicURL.Scheme != "https") {
		return "", fmt.Errorf("APP_DOMAIN inválida")
	}
	return strings.TrimRight(parsedPublicURL.Scheme+"://"+parsedPublicURL.Host, "/"), nil
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
