package instances

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sophus/internal/repo"
	"sophus/pkg/http/requests"
	"sophus/utils/env"
	"strings"
	"time"

	"github.com/google/uuid"
)

type InstanceEVO struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Connected     bool      `json:"connected"`
	WebhookURL    string    `json:"webhookURL"`
	InstanceID    uuid.UUID `json:"instanceId"`
	ConnectionKey uuid.UUID `json:"connectionKey"` // real evogo api key
	APIToken      string    `json:"apiToken"`      // used by customer to api calls
}

type InstanceEvoResponse struct {
	Data struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	} `json:"data"`
	Message string `json:"message"`
}

type QRCode struct {
	Code     string
	QRCode   string
	Count    int
	MaxCount int
}

type Status struct {
	Connected bool
	State     string
	Number    string
}

type Contact struct {
	JID          string `json:"Jid"`
	Found        bool   `json:"Found"`
	FirstName    string `json:"FirstName"`
	FullName     string `json:"FullName"`
	PushName     string `json:"PushName"`
	BusinessName string `json:"BusinessName"`
}

func (contact Contact) PreferredName() string {
	for _, value := range []string{contact.FullName, contact.FirstName, contact.BusinessName, contact.PushName} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// Found means the identity exists in WhatsApp's cache, not necessarily in the
// phone's address book. Group participants commonly have only PushName or a
// resolved business name, which must not make them importable saved contacts.
func (contact Contact) IsAddressBookContact() bool {
	return strings.TrimSpace(contact.FullName) != "" || strings.TrimSpace(contact.FirstName) != ""
}

func (i *InstanceEVO) GetContacts() ([]Contact, error) {
	r := requests.Request{
		URL:     repo.ApiBaseURL + "/user/contacts",
		Headers: map[string]string{"apikey": i.APIToken},
		Method:  "GET", Timeout: 45 * time.Second, MaxResponseBytes: 16 << 20,
	}
	if err := r.Do(); err != nil {
		return nil, fmt.Errorf("Evolution GO contacts request failed: %w", err)
	}
	var response struct {
		Data []Contact `json:"data"`
	}
	if err := json.Unmarshal(r.Response.Body, &response); err != nil {
		return nil, fmt.Errorf("Evolution GO contacts returned invalid JSON: %w", err)
	}
	return response.Data, nil
}

func (i *InstanceEVO) Create() error {
	r := requests.Request{
		URL: repo.ApiBaseURL + "/instance/create",
		Payload: map[string]any{
			"name":          i.Name,
			"token":         i.APIToken,
			"instanceId":    i.InstanceID.String(),
			"webhook":       i.WebhookURL,
			"webhookEvents": []string{"QRCODE", "CONNECTION", "MESSAGE", "BUTTON_CLICK", "PICTURE", "READ_RECEIPT"},
		},
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       env.Backend["WPP_API_GLOBAL_TOKEN"],
		},
		Method: "POST",
	}
	err := r.Do()
	if err != nil {
		return err
	}
	var response InstanceEvoResponse
	if err := json.Unmarshal(r.Response.Body, &response); err != nil {
		return err
	}
	if response.Data.ID != "" {
		instanceID, err := uuid.Parse(response.Data.ID)
		if err != nil {
			return fmt.Errorf("invalid Evolution GO instance ID: %w", err)
		}
		i.InstanceID = instanceID
	}
	if response.Data.Token != "" {
		connectionKey, err := uuid.Parse(response.Data.Token)
		if err != nil {
			return fmt.Errorf("invalid Evolution GO instance token: %w", err)
		}
		i.ConnectionKey = connectionKey
		i.APIToken = response.Data.Token
	}

	return nil
}

func (i *InstanceEVO) Delete() error {
	r := requests.Request{
		URL: repo.ApiBaseURL + "/instance/delete/" + i.InstanceID.String(),
		Headers: map[string]string{
			"apikey": env.Backend["WPP_API_GLOBAL_TOKEN"],
		},
		Method:           "DELETE",
		Timeout:          10 * time.Second,
		MaxResponseBytes: 64 << 10,
	}
	err := r.Do()
	if err == nil || r.StatusCode == http.StatusNotFound {
		return nil
	}

	deleteErr := evolutionRequestError("delete instance", &r, err)
	exists, verifyErr := evolutionInstanceExists(i.InstanceID)
	if verifyErr != nil {
		return fmt.Errorf("%w; could not verify remote state: %v", deleteErr, verifyErr)
	}
	if !exists {
		return nil
	}
	return deleteErr
}

func evolutionInstanceExists(instanceID uuid.UUID) (bool, error) {
	r := requests.Request{
		URL: repo.ApiBaseURL + "/instance/all",
		Headers: map[string]string{
			"apikey": env.Backend["WPP_API_GLOBAL_TOKEN"],
		},
		Method:           http.MethodGet,
		Timeout:          10 * time.Second,
		MaxResponseBytes: 16 << 20,
	}
	if err := r.Do(); err != nil {
		return false, evolutionRequestError("list instances", &r, err)
	}
	var response struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(r.Response.Body, &response); err != nil {
		return false, fmt.Errorf("list instances returned invalid JSON: %w", err)
	}
	if len(response.Data) == 0 || string(response.Data) == "null" {
		return false, fmt.Errorf("list instances response is missing data")
	}
	var remoteInstances []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Data, &remoteInstances); err != nil {
		return false, fmt.Errorf("list instances returned invalid data: %w", err)
	}
	for _, instance := range remoteInstances {
		if instance.ID == instanceID.String() {
			return true, nil
		}
	}
	return false, nil
}

func evolutionRequestError(operation string, request *requests.Request, err error) error {
	body := strings.Join(strings.Fields(string(request.Response.Body)), " ")
	if len(body) > 2048 {
		body = body[:2048] + "..."
	}
	if body == "" {
		return fmt.Errorf("Evolution GO %s: %w", operation, err)
	}
	return fmt.Errorf("Evolution GO %s: %w: %s", operation, err, body)
}

func (i *InstanceEVO) Connect() error {
	payload := map[string]any{
		"subscribe": []string{"QRCODE", "CONNECTION", "MESSAGE", "BUTTON_CLICK", "PICTURE"},
		"immediate": true,
	}
	if i.WebhookURL != "" {
		payload["webhookUrl"] = i.WebhookURL
	}
	r := requests.Request{
		URL:     repo.ApiBaseURL + "/instance/connect",
		Payload: payload,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       i.APIToken,
		},
		Method:  "POST",
		Timeout: 10 * time.Second,
	}
	if err := r.Do(); err != nil {
		return evolutionRequestError("connect instance", &r, err)
	}
	return nil
}

func (i *InstanceEVO) Reconnect() error {
	return i.ReconnectWithStart(nil)
}

// ReconnectWithStart invokes start only after acquiring the per-instance gate.
func (i *InstanceEVO) ReconnectWithStart(start func() error) error {
	key := i.InstanceID.String() + ":" + i.APIToken
	if !beginReconnect(key) {
		return ErrReconnectInProgress
	}
	defer endReconnect(key)
	if start != nil {
		if err := start(); err != nil {
			return err
		}
	}
	r := requests.Request{
		URL: repo.ApiBaseURL + "/instance/reconnect",
		Headers: map[string]string{
			"apikey": i.APIToken,
		},
		Method:  "POST",
		Timeout: 10 * time.Second,
	}
	err := r.Do()
	if err == nil {
		return nil
	}
	reconnectErr := evolutionRequestError("reconnect instance", &r, err)
	// Recover a retained disconnected client without deleting the instance or
	// changing its credentials. Never do this after an authentication failure.
	if r.StatusCode != http.StatusInternalServerError && r.StatusCode != http.StatusNotFound {
		return reconnectErr
	}
	status, statusErr := i.GetStatus()
	if statusErr != nil {
		return fmt.Errorf("%w; could not verify connection: %v", reconnectErr, statusErr)
	}
	if status.Connected {
		return nil
	}
	if err := i.Connect(); err != nil {
		return fmt.Errorf("%w; connect recovery failed: %v", reconnectErr, err)
	}
	return nil
}

func (i *InstanceEVO) GetQRCode() (QRCode, error) {
	r := requests.Request{
		URL: repo.ApiBaseURL + "/instance/qr",
		Headers: map[string]string{
			"apikey": i.APIToken,
		},
		Method: "GET",
	}
	if err := r.Do(); err != nil {
		return QRCode{}, err
	}
	var response struct {
		Data struct {
			QRCode   string `json:"qrcode"`
			Code     string `json:"code"`
			Count    int    `json:"count"`
			MaxCount int    `json:"maxCount"`
		} `json:"data"`
	}
	if err := json.Unmarshal(r.Response.Body, &response); err != nil {
		return QRCode{}, err
	}
	result := QRCode{
		Code:     response.Data.Code,
		QRCode:   response.Data.QRCode,
		Count:    response.Data.Count,
		MaxCount: response.Data.MaxCount,
	}
	if strings.HasPrefix(response.Data.Code, "data:image/") {
		result.Code = response.Data.QRCode
		result.QRCode = response.Data.Code
	}
	if result.QRCode == "" {
		return QRCode{}, fmt.Errorf("Evolution GO returned an empty QR Code")
	}
	return result, nil
}

func (i *InstanceEVO) GetStatus() (Status, error) {
	const attempts = 2
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		r := requests.Request{
			URL: repo.ApiBaseURL + "/instance/status",
			Headers: map[string]string{
				"apikey": i.APIToken,
			},
			Method:  "GET",
			Timeout: 5 * time.Second,
		}
		if err := r.Do(); err != nil {
			return Status{}, fmt.Errorf("Evolution GO status request failed: %w", err)
		}
		body := bytes.TrimSpace(r.Response.Body)
		if len(body) == 0 {
			lastErr = fmt.Errorf("Evolution GO status returned an empty body with HTTP %d", r.StatusCode)
		} else {
			var response map[string]interface{}
			if err := json.Unmarshal(body, &response); err != nil {
				lastErr = fmt.Errorf("Evolution GO status returned invalid JSON with HTTP %d: %w", r.StatusCode, err)
			} else if status, ok := parseStatus(response); ok {
				return status, nil
			} else {
				return Status{}, fmt.Errorf("Evolution GO returned an unknown status response with HTTP %d: %s", r.StatusCode, string(body))
			}
		}
		if attempt < attempts {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return Status{}, lastErr
}

func parseStatus(value interface{}) (Status, bool) {
	object, ok := value.(map[string]interface{})
	if !ok {
		return Status{}, false
	}
	state, hasState := statusState(object)
	if hasState && state == "disconnected" {
		return Status{Connected: false, State: state, Number: statusNumber(object)}, true
	}
	connected, hasConnected := statusBool(object, "connected", "Connected", "isConnected", "is_connected")
	loggedIn, hasLoggedIn := statusBool(object, "loggedIn", "LoggedIn", "logged_in", "isLoggedIn", "is_logged_in")
	if hasConnected || hasLoggedIn {
		active := (!hasConnected || connected) && (!hasLoggedIn || loggedIn)
		return Status{Connected: active, State: connectedState(active), Number: statusNumber(object)}, true
	}
	if hasState {
		return Status{Connected: state == "connected", State: state, Number: statusNumber(object)}, true
	}
	for _, key := range []string{"data", "instance", "connection"} {
		if nested, exists := object[key]; exists {
			if status, found := parseStatus(nested); found {
				if status.Number == "" {
					status.Number = statusNumber(object)
				}
				return status, true
			}
		}
	}
	return Status{}, false
}

func statusBool(object map[string]interface{}, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, exists := object[key].(bool); exists {
			return value, true
		}
	}
	return false, false
}

func statusState(object map[string]interface{}) (string, bool) {
	for _, key := range []string{"status", "state", "connectionState", "connection_state"} {
		raw, exists := object[key].(string)
		if !exists {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "connected", "open", "online", "ready":
			return "connected", true
		case "disconnected", "close", "closed", "offline", "loggedout", "logged_out":
			return "disconnected", true
		case "connecting", "pairing", "qrcode", "qr_code":
			return "connecting", true
		}
	}
	return "", false
}

func statusNumber(object map[string]interface{}) string {
	for _, key := range []string{"number", "phone", "jid", "JID"} {
		if value, ok := object[key].(string); ok && value != "" {
			return strings.Split(value, "@")[0]
		}
	}
	return ""
}

func connectedState(connected bool) string {
	if connected {
		return "connected"
	}
	return "disconnected"
}

//stmt, err := repo.DB.Prepare("RETURNING id;")
//if err != nil {
//	return err
//}
//defer stmt.Close()
//err = stmt.QueryRow()
