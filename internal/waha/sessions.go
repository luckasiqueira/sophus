package waha

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/google/uuid"
)

const (
	statusWorking    = "WORKING"
	statusScanQRCode = "SCAN_QR_CODE"
	statusStopped    = "STOPPED"
	statusFailed     = "FAILED"

	metadataInstanceID   = "sophus.instance_id"
	metadataConnectionID = "sophus.connection_id"
)

// webhookEvents are all Sophus needs: engine.event carries whatsmeow's raw
// events (same JSON Evolution GO forwards) and session.status reports pairing.
var webhookEvents = []string{"engine.event", "session.status"}

type sessionInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Config struct {
		Metadata map[string]string `json:"metadata"`
		Webhooks []struct {
			URL    string   `json:"url"`
			Events []string `json:"events"`
		} `json:"webhooks"`
	} `json:"config"`
	Me *struct {
		ID       string `json:"id"`
		PushName string `json:"pushName"`
	} `json:"me"`
}

var errSessionNotFound = errors.New("WAHA session not found")

func (b *Bridge) getSession(ctx context.Context, session string) (sessionInfo, apiResponse, error) {
	response, err := b.Client.Do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(session), nil, nil, "")
	if err != nil {
		return sessionInfo{}, response, err
	}
	if response.Status == http.StatusNotFound {
		return sessionInfo{}, response, errSessionNotFound
	}
	if !response.OK() {
		return sessionInfo{}, response, fmt.Errorf("WAHA session lookup failed with status %d: %s", response.Status, errorText(response.Body))
	}
	var info sessionInfo
	if err := json.Unmarshal(response.Body, &info); err != nil {
		return sessionInfo{}, response, fmt.Errorf("invalid WAHA session response: %w", err)
	}
	return info, response, nil
}

func sessionConfig(connection repo.ConnectionEVO, webhookURL string) map[string]any {
	metadata := map[string]string{metadataInstanceID: connection.InstanceID.String()}
	if connection.Id > 0 {
		metadata[metadataConnectionID] = strconv.Itoa(connection.Id)
	}
	return map[string]any{
		"metadata": metadata,
		"webhooks": []map[string]any{{
			"url":    webhookURL,
			"events": webhookEvents,
			// The Evolution apikey signs every webhook (X-Webhook-Hmac).
			"hmac":    map[string]string{"key": connection.EvolutionAPIKey()},
			"retries": map[string]any{"policy": "exponential", "delaySeconds": 2, "attempts": 8},
		}},
	}
}

func webhookConfigured(info sessionInfo, webhookURL string) bool {
	if len(info.Config.Webhooks) != 1 || info.Config.Webhooks[0].URL != webhookURL {
		return false
	}
	have := map[string]bool{}
	for _, event := range info.Config.Webhooks[0].Events {
		have[event] = true
	}
	for _, event := range webhookEvents {
		if !have[event] {
			return false
		}
	}
	return info.Config.Metadata[metadataInstanceID] != ""
}

// ensureSession creates the WAHA session for a connection, or repairs its
// webhook configuration. It never restarts a session whose config is current.
func (b *Bridge) ensureSession(ctx context.Context, connection repo.ConnectionEVO, webhookURL string) (sessionInfo, error) {
	if webhookURL == "" {
		var err error
		if webhookURL, err = b.webhookURL(connection); err != nil {
			return sessionInfo{}, err
		}
	}
	session := SessionName(connection.InstanceID)
	info, _, err := b.getSession(ctx, session)
	switch {
	case errors.Is(err, errSessionNotFound):
		response, createErr := b.Client.Do(ctx, http.MethodPost, "/api/sessions", nil, map[string]any{
			"name": session, "start": false, "config": sessionConfig(connection, webhookURL),
		}, "")
		if createErr != nil {
			return sessionInfo{}, createErr
		}
		if !response.OK() {
			return sessionInfo{}, fmt.Errorf("create WAHA session: status %d: %s", response.Status, errorText(response.Body))
		}
		info, _, err = b.getSession(ctx, session)
		return info, err
	case err != nil:
		return sessionInfo{}, err
	}
	if webhookConfigured(info, webhookURL) {
		return info, nil
	}
	response, err := b.Client.Do(ctx, http.MethodPut, "/api/sessions/"+url.PathEscape(session), nil, map[string]any{
		"config": sessionConfig(connection, webhookURL),
	}, "")
	if err != nil {
		return sessionInfo{}, err
	}
	if !response.OK() {
		return sessionInfo{}, fmt.Errorf("update WAHA session: status %d: %s", response.Status, errorText(response.Body))
	}
	info, _, err = b.getSession(ctx, session)
	return info, err
}

func (b *Bridge) startSession(ctx context.Context, session string) error {
	response, err := b.Client.Do(ctx, http.MethodPost, "/api/sessions/"+url.PathEscape(session)+"/start", nil, nil, "")
	if err != nil {
		return err
	}
	if response.OK() {
		return nil
	}
	// Starting an already running session is not an error for Sophus.
	if info, _, lookupErr := b.getSession(ctx, session); lookupErr == nil && info.Status != statusStopped && info.Status != statusFailed {
		return nil
	}
	return fmt.Errorf("start WAHA session: status %d: %s", response.Status, errorText(response.Body))
}

func (b *Bridge) webhookURL(connection repo.ConnectionEVO) (string, error) {
	if connection.Webhook == uuid.Nil {
		return "", errors.New("connection has no webhook ID")
	}
	publicURL := publicURLFromEnv
	if b.PublicURL != nil {
		publicURL = b.PublicURL
	}
	base, err := publicURL()
	if err != nil {
		return "", err
	}
	return base + "/webhook/" + connection.Webhook.String(), nil
}

func publicURLFromEnv() (string, error) {
	publicURL := strings.TrimSpace(env.Backend["APP_DOMAIN"])
	if publicURL == "" {
		return "", errors.New("APP_DOMAIN não configurada")
	}
	if !strings.Contains(publicURL, "://") {
		publicURL = "https://" + publicURL
	}
	return strings.TrimRight(publicURL, "/"), nil
}

// POST /instance/create {name, token, instanceId, webhook}
func (b *Bridge) createInstance(ctx context.Context, payload []byte) result {
	var request struct {
		Name       string `json:"name"`
		Token      string `json:"token"`
		InstanceID string `json:"instanceId"`
		Webhook    string `json:"webhook"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	instanceID, err := uuid.Parse(request.InstanceID)
	if err != nil || strings.TrimSpace(request.Token) == "" || strings.TrimSpace(request.Webhook) == "" {
		return failure(http.StatusBadRequest, "instanceId, token and webhook are required")
	}
	connection, lookupErr := b.connection(request.Token)
	if lookupErr != nil || connection.InstanceID != instanceID {
		connection = repo.ConnectionEVO{InstanceID: instanceID, APIToken: request.Token, Name: request.Name}
	}
	if _, err := b.ensureSession(ctx, connection, request.Webhook); err != nil {
		return failure(http.StatusBadGateway, err.Error())
	}
	return success(map[string]any{
		"data":    map[string]string{"id": instanceID.String(), "token": request.Token, "name": request.Name},
		"message": "success",
	})
}

// POST /instance/connect {webhookUrl, subscribe, immediate}
func (b *Bridge) connect(ctx context.Context, connection repo.ConnectionEVO, payload []byte) result {
	var request struct {
		WebhookURL string `json:"webhookUrl"`
	}
	_ = json.Unmarshal(payload, &request)
	info, err := b.ensureSession(ctx, connection, strings.TrimSpace(request.WebhookURL))
	if err != nil {
		return failure(http.StatusBadGateway, err.Error())
	}
	if info.Status == "" || info.Status == statusStopped || info.Status == statusFailed {
		if err := b.startSession(ctx, info.Name); err != nil {
			return failure(http.StatusBadGateway, err.Error())
		}
	}
	return success(map[string]any{"message": "success"})
}

// POST /instance/reconnect restarts the WhatsApp client, recreating the WAHA
// session from stored data when it does not exist (e.g. after a migration).
func (b *Bridge) reconnect(ctx context.Context, connection repo.ConnectionEVO) result {
	session := SessionName(connection.InstanceID)
	info, response, err := b.getSession(ctx, session)
	switch {
	case errors.Is(err, errSessionNotFound):
		info, err = b.ensureSession(ctx, connection, "")
		if err != nil {
			return failure(http.StatusInternalServerError, err.Error())
		}
		if err := b.startSession(ctx, info.Name); err != nil {
			return failure(http.StatusInternalServerError, err.Error())
		}
		return success(map[string]any{"message": "success"})
	case err != nil:
		if response.Status != 0 {
			return fromWAHA(response, nil)
		}
		return result{err: err}
	}
	if info.Status == statusStopped {
		if err := b.startSession(ctx, session); err != nil {
			return failure(http.StatusInternalServerError, err.Error())
		}
		return success(map[string]any{"message": "success"})
	}
	restart, err := b.Client.Do(ctx, http.MethodPost, "/api/sessions/"+url.PathEscape(session)+"/restart", nil, nil, "")
	if err != nil || !restart.OK() {
		return fromWAHA(restart, err)
	}
	return success(map[string]any{"message": "success"})
}

// GET /instance/qr -> {data:{qrcode:"data:image/png;base64,...", code}}
func (b *Bridge) qrCode(ctx context.Context, connection repo.ConnectionEVO) result {
	session := SessionName(connection.InstanceID)
	info, response, err := b.getSession(ctx, session)
	if errors.Is(err, errSessionNotFound) {
		return failure(http.StatusNotFound, "instance not found")
	}
	if err != nil {
		return fromWAHA(response, err)
	}
	switch info.Status {
	case statusWorking:
		return failure(http.StatusBadRequest, "instance already connected")
	case statusStopped, statusFailed:
		if err := b.startSession(ctx, session); err != nil {
			return failure(http.StatusBadGateway, err.Error())
		}
		return failure(http.StatusNotFound, "QR Code not ready")
	case statusScanQRCode:
	default:
		return failure(http.StatusNotFound, "QR Code not ready")
	}
	path := "/api/" + url.PathEscape(session) + "/auth/qr"
	image, err := b.Client.Do(ctx, http.MethodGet, path, url.Values{"format": {"image"}}, nil, "application/json")
	if err != nil || !image.OK() {
		return fromWAHA(image, err)
	}
	var picture struct {
		Mimetype string `json:"mimetype"`
		Data     string `json:"data"`
	}
	if err := json.Unmarshal(image.Body, &picture); err != nil || picture.Data == "" {
		return failure(http.StatusBadGateway, "WAHA returned an empty QR Code")
	}
	if _, err := base64.StdEncoding.DecodeString(picture.Data); err != nil {
		return failure(http.StatusBadGateway, "WAHA returned an invalid QR Code image")
	}
	if picture.Mimetype == "" {
		picture.Mimetype = "image/png"
	}
	code := ""
	if raw, rawErr := b.Client.Do(ctx, http.MethodGet, path, url.Values{"format": {"raw"}}, nil, "application/json"); rawErr == nil && raw.OK() {
		var value struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(raw.Body, &value) == nil {
			code = value.Value
		}
	}
	return success(map[string]any{
		"data": map[string]any{
			"qrcode": "data:" + picture.Mimetype + ";base64," + picture.Data,
			"code":   code,
		},
		"message": "success",
	})
}

// GET /instance/status -> {data:{Connected, LoggedIn, Name, jid}}
func (b *Bridge) status(ctx context.Context, session string) result {
	info, response, err := b.getSession(ctx, session)
	if errors.Is(err, errSessionNotFound) {
		return failure(http.StatusNotFound, "instance not found")
	}
	if err != nil {
		return fromWAHA(response, err)
	}
	working := info.Status == statusWorking
	data := map[string]any{"Connected": working, "LoggedIn": working}
	if info.Me != nil {
		data["Name"] = info.Me.PushName
		data["jid"] = whatsmeowJID(cleanDevice(info.Me.ID))
	}
	return success(map[string]any{"data": data, "message": "success"})
}

// DELETE /instance/logout: unlink the device and drop its stored auth keys,
// keeping the session so a new QR Code can pair it again.
func (b *Bridge) logout(ctx context.Context, session string) result {
	response, err := b.Client.Do(ctx, http.MethodPost, "/api/sessions/"+url.PathEscape(session)+"/logout", nil, nil, "")
	if err != nil {
		return result{err: err}
	}
	if !response.OK() && response.Status != http.StatusNotFound {
		return fromWAHA(response, nil)
	}
	return success(map[string]any{"message": "success"})
}

// DELETE /instance/delete/{instanceId}: unlink the device, then remove it.
func (b *Bridge) deleteInstance(ctx context.Context, rawInstanceID string) result {
	instanceID, err := uuid.Parse(strings.TrimSpace(rawInstanceID))
	if err != nil {
		return failure(http.StatusBadRequest, "invalid instance ID")
	}
	session := url.PathEscape(SessionName(instanceID))
	_, _ = b.Client.Do(ctx, http.MethodPost, "/api/sessions/"+session+"/logout", nil, nil, "")
	response, err := b.Client.Do(ctx, http.MethodDelete, "/api/sessions/"+session, nil, nil, "")
	if err != nil {
		return result{err: err}
	}
	b.forgetInstance(instanceID)
	if !response.OK() {
		return fromWAHA(response, nil)
	}
	return success(map[string]any{"message": "success"})
}

func (b *Bridge) forgetInstance(instanceID uuid.UUID) {
	b.connections.Range(func(key, value any) bool {
		if value.(cachedConnection).connection.InstanceID == instanceID {
			b.connections.Delete(key)
		}
		return true
	})
}

// GET /instance/all -> {data:[{id}]}
func (b *Bridge) listInstances(ctx context.Context) result {
	response, err := b.Client.Do(ctx, http.MethodGet, "/api/sessions", url.Values{"all": {"true"}}, nil, "")
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	var sessions []sessionInfo
	if err := json.Unmarshal(response.Body, &sessions); err != nil {
		return failure(http.StatusBadGateway, "invalid WAHA sessions response")
	}
	instances := make([]map[string]any, 0, len(sessions))
	for _, session := range sessions {
		id := session.Config.Metadata[metadataInstanceID]
		if id == "" {
			if parsed, ok := instanceIDFromSession(session.Name); ok {
				id = parsed.String()
			}
		}
		if id == "" {
			continue
		}
		instances = append(instances, map[string]any{
			"id": id, "name": session.Name, "connected": session.Status == statusWorking,
		})
	}
	return success(map[string]any{"data": instances, "message": "success"})
}
