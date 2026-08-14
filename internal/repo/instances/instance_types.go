package instances

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func (i *InstanceEVO) Create() error {
	r := requests.Request{
		URL: repo.ApiBaseURL + "/instance/create",
		Payload: map[string]any{
			"name":          i.Name,
			"token":         i.APIToken,
			"instanceId":    i.InstanceID.String(),
			"webhook":       i.WebhookURL,
			"webhookEvents": []string{"QRCODE", "CONNECTION", "MESSAGE", "BUTTON_CLICK"},
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

func (i *InstanceEVO) Connect() error {
	r := requests.Request{
		URL: repo.ApiBaseURL + "/instance/connect",
		Payload: map[string]any{
			"webhookUrl": i.WebhookURL,
			"subscribe":  []string{"QRCODE", "CONNECTION", "MESSAGE", "BUTTON_CLICK"},
			"immediate":  true,
		},
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       i.APIToken,
		},
		Method: "POST",
	}
	return r.Do()
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
	for _, key := range []string{"connected", "isConnected", "is_connected"} {
		if connected, exists := object[key].(bool); exists {
			return Status{Connected: connected, State: connectedState(connected), Number: statusNumber(object)}, true
		}
	}
	for _, key := range []string{"status", "state", "connectionState", "connection_state"} {
		if raw, exists := object[key].(string); exists {
			state := strings.ToLower(strings.TrimSpace(raw))
			switch state {
			case "connected", "open", "online", "ready":
				return Status{Connected: true, State: "connected", Number: statusNumber(object)}, true
			case "disconnected", "close", "closed", "offline", "loggedout", "logged_out":
				return Status{Connected: false, State: "disconnected", Number: statusNumber(object)}, true
			}
		}
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
