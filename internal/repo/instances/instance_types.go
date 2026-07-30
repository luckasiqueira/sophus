package instances

import (
	"encoding/json"
	"fmt"
	"sophus/internal/repo"
	"sophus/pkg/http/requests"
	"sophus/utils/env"
	"strings"

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
	if err := json.Unmarshal(r.Body, &response); err != nil {
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

//stmt, err := repo.DB.Prepare("RETURNING id;")
//if err != nil {
//	return err
//}
//defer stmt.Close()
//err = stmt.QueryRow()
