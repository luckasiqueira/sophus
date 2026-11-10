package waha

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"sophus/internal/repo"
	"sophus/internal/waprovider"
	"sophus/pkg/http/requests"
)

const (
	connectionCacheTTL = time.Minute
	groupCacheTTL      = 10 * time.Minute
	maxTypingDelay     = 15 * time.Second
)

// Bridge implements the Evolution GO endpoints Sophus uses on top of WAHA.
type Bridge struct {
	Client *Client
	// Resolve maps the Evolution "apikey" to its connection.
	Resolve func(key string) (repo.ConnectionEVO, error)
	// PublicURL is Sophus' public base URL, used to build webhook URLs for
	// sessions that must be (re)created from stored connection data.
	PublicURL func() (string, error)
	Now       func() time.Time
	Sleep     func(context.Context, time.Duration)

	connections sync.Map // key -> cachedConnection
	groups      sync.Map // session|jid -> cachedGroup
}

type cachedConnection struct {
	connection repo.ConnectionEVO
	expires    time.Time
}

type cachedGroup struct {
	data    json.RawMessage
	expires time.Time
}

var defaultBridge = &Bridge{}
var defaultOnce sync.Once

func bridge() *Bridge {
	defaultOnce.Do(func() {
		defaultBridge.Client = newClientFromEnv()
		defaultBridge.Resolve = repo.GetConnectionByEvolutionKey
		defaultBridge.PublicURL = publicURLFromEnv
	})
	return defaultBridge
}

// Intercept is registered with requests.SetInterceptor when WAHA is selected.
func Intercept(r *requests.Request) (bool, int, []byte, error) {
	return bridge().Intercept(r)
}

// Intercept serves requests addressed to waprovider.BridgeBaseURL.
func (b *Bridge) Intercept(r *requests.Request) (bool, int, []byte, error) {
	if !strings.HasPrefix(r.URL, waprovider.BridgeBaseURL) {
		return false, 0, nil, nil
	}
	path := strings.TrimPrefix(r.URL, waprovider.BridgeBaseURL)
	if index := strings.Index(path, "?"); index != -1 {
		path = path[:index]
	}
	payload := r.RequestBody
	if payload == nil && r.Payload != nil {
		encoded, err := json.Marshal(r.Payload)
		if err != nil {
			return true, 0, nil, err
		}
		payload = encoded
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultCallLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res := b.route(ctx, strings.ToUpper(r.Method), path, headerValue(r.Headers, "apikey"), payload)
	return true, res.status, res.body, res.err
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

type result struct {
	status int
	body   []byte
	err    error
}

func success(value any) result {
	body, err := json.Marshal(value)
	if err != nil {
		return result{err: err}
	}
	return result{status: http.StatusOK, body: body}
}

func failure(status int, message string) result {
	body, _ := json.Marshal(map[string]string{"error": message, "message": message})
	return result{status: status, body: body}
}

// fromWAHA converts a failed WAHA response into an Evolution-style error.
func fromWAHA(response apiResponse, err error) result {
	if err != nil {
		return result{err: err}
	}
	status := response.Status
	if status < 400 {
		status = http.StatusBadGateway
	}
	return failure(status, errorText(response.Body))
}

func (b *Bridge) route(ctx context.Context, method, path, apiKey string, payload []byte) result {
	switch {
	case method == http.MethodPost && path == "/instance/create":
		return b.createInstance(ctx, payload)
	case method == http.MethodGet && path == "/instance/all":
		return b.listInstances(ctx)
	case method == http.MethodDelete && strings.HasPrefix(path, "/instance/delete/"):
		return b.deleteInstance(ctx, strings.TrimPrefix(path, "/instance/delete/"))
	case method == http.MethodGet && strings.HasPrefix(path, "/instance/info/"):
		// Picture/receipt events are always delivered through engine.event,
		// so Evolution's subscription repair must never try to change them.
		return success(map[string]any{"data": map[string]string{"events": "ALL"}, "message": "success"})
	}
	connection, err := b.connection(apiKey)
	if err != nil {
		// Evolution GO answers unknown instance tokens with 401.
		return failure(http.StatusUnauthorized, "instance not found for apikey")
	}
	session := SessionName(connection.InstanceID)
	switch {
	case method == http.MethodPost && path == "/instance/connect":
		return b.connect(ctx, connection, payload)
	case method == http.MethodPost && path == "/instance/reconnect":
		return b.reconnect(ctx, connection)
	case method == http.MethodGet && path == "/instance/qr":
		return b.qrCode(ctx, connection)
	case method == http.MethodGet && path == "/instance/status":
		return b.status(ctx, session)
	case method == http.MethodGet && path == "/user/contacts":
		return b.contacts(ctx, session)
	case method == http.MethodPost && path == "/user/check":
		return b.checkUser(ctx, session, payload)
	case method == http.MethodPost && path == "/user/avatar":
		return b.avatar(ctx, session, payload)
	case method == http.MethodPost && path == "/group/info":
		return b.groupInfoEndpoint(ctx, session, payload)
	case method == http.MethodPost && path == "/send/text":
		return b.sendText(ctx, session, payload)
	case method == http.MethodPost && path == "/send/media":
		return b.sendMedia(ctx, session, payload)
	case method == http.MethodPost && path == "/send/button":
		return b.sendButtons(ctx, session, payload)
	case method == http.MethodPost && path == "/message/react":
		return b.react(ctx, session, payload)
	case method == http.MethodPost && path == "/message/edit":
		return b.edit(ctx, session, payload)
	case method == http.MethodPost && path == "/message/markread":
		return b.markRead(ctx, session, payload)
	case method == http.MethodPost && path == "/message/downloadmedia":
		return downloadMediaEndpoint(ctx, payload)
	case method == http.MethodPost && path == "/message/status":
		// WAHA needs the chat to look up a stored message; Sophus only uses
		// this legacy lookup as a best-effort fallback.
		return failure(http.StatusNotFound, "message status lookup is not available with WAHA")
	}
	return failure(http.StatusNotFound, "endpoint not supported by the WAHA bridge: "+method+" "+path)
}

func (b *Bridge) connection(key string) (repo.ConnectionEVO, error) {
	key = strings.TrimSpace(key)
	if key == "" || b.Resolve == nil {
		return repo.ConnectionEVO{}, errors.New("missing apikey")
	}
	now := b.now()
	if cached, ok := b.connections.Load(key); ok {
		entry := cached.(cachedConnection)
		if now.Before(entry.expires) {
			return entry.connection, nil
		}
	}
	connection, err := b.Resolve(key)
	if err != nil {
		b.connections.Delete(key)
		return repo.ConnectionEVO{}, err
	}
	b.connections.Store(key, cachedConnection{connection: connection, expires: now.Add(connectionCacheTTL)})
	return connection, nil
}

func (b *Bridge) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Bridge) sleep(ctx context.Context, duration time.Duration) {
	if b.Sleep != nil {
		b.Sleep(ctx, duration)
		return
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// typing mirrors Evolution GO's "delay": show "typing..." before sending.
func (b *Bridge) typing(ctx context.Context, session, chatID string, delayMilliseconds int) {
	if delayMilliseconds <= 0 {
		return
	}
	delay := time.Duration(delayMilliseconds) * time.Millisecond
	if delay > maxTypingDelay {
		delay = maxTypingDelay
	}
	request := map[string]string{"session": session, "chatId": chatID}
	_, _ = b.Client.Do(ctx, http.MethodPost, "/api/startTyping", nil, request, "")
	b.sleep(ctx, delay)
	_, _ = b.Client.Do(ctx, http.MethodPost, "/api/stopTyping", nil, request, "")
}

func decode(payload []byte, target any) error {
	if len(payload) == 0 {
		return errors.New("empty request payload")
	}
	return json.Unmarshal(payload, target)
}

// APIURL is the configured WAHA base URL (for startup logs).
func APIURL() string {
	return bridge().Client.BaseURL
}
