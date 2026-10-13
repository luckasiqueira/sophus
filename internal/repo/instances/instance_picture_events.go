package instances

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"sophus/internal/repo"
	"sophus/pkg/http/requests"
	"sophus/utils/env"
)

// Existing instances can update subscriptions through Connect without
// restarting their WhatsApp session. Preserve other event/channel settings.
func (i *InstanceEVO) EnsurePictureSubscription() error {
	key := env.Backend["WPP_API_GLOBAL_TOKEN"]
	if key == "" {
		key = i.APIToken
	}
	info := requests.Request{
		URL: repo.ApiBaseURL + "/instance/info/" + i.InstanceID.String(), Method: "GET",
		Headers: map[string]string{"apikey": key}, Timeout: 10 * time.Second, MaxResponseBytes: 512 << 10,
	}
	if err := info.Do(); err != nil {
		return fmt.Errorf("read instance picture subscription: %w", err)
	}
	var response struct {
		Data struct {
			Events          string `json:"events"`
			Webhook         string `json:"webhook"`
			RabbitmqEnable  string `json:"rabbitmqEnable"`
			WebSocketEnable string `json:"websocketEnable"`
			NatsEnable      string `json:"natsEnable"`
		} `json:"data"`
	}
	if err := json.Unmarshal(info.Response.Body, &response); err != nil {
		return fmt.Errorf("invalid instance settings: %w", err)
	}
	events := []string{}
	hasPicture, hasReceipts, hasMessages := false, false, false
	for _, event := range strings.Split(response.Data.Events, ",") {
		event = strings.ToUpper(strings.TrimSpace(event))
		if event == "ALL" {
			return nil
		}
		hasPicture = hasPicture || event == "PICTURE"
		hasReceipts = hasReceipts || event == "READ_RECEIPT"
		hasMessages = hasMessages || event == "MESSAGE"
		if event != "" {
			events = append(events, event)
		}
	}
	if len(events) == 0 {
		events = []string{"QRCODE", "CONNECTION", "MESSAGE", "BUTTON_CLICK"}
		hasMessages = true
	}
	if hasPicture && hasReceipts && hasMessages {
		return nil
	}
	if !hasPicture {
		events = append(events, "PICTURE")
	}
	if !hasReceipts {
		events = append(events, "READ_RECEIPT")
	}
	if !hasMessages {
		events = append(events, "MESSAGE")
	}
	webhook := response.Data.Webhook
	if webhook == "" {
		webhook = i.WebhookURL
	}
	if webhook == "" {
		return fmt.Errorf("instance has no configured webhook")
	}
	update := requests.Request{
		URL: repo.ApiBaseURL + "/instance/connect", Method: "POST", Timeout: 10 * time.Second,
		Payload: map[string]any{"webhookUrl": webhook, "subscribe": events, "immediate": true,
			"rabbitmqEnable": response.Data.RabbitmqEnable, "websocketEnable": response.Data.WebSocketEnable, "natsEnable": response.Data.NatsEnable},
		Headers: map[string]string{"Content-Type": "application/json", "apikey": i.APIToken},
	}
	if err := update.Do(); err != nil {
		return fmt.Errorf("subscribe to contact picture events: %w", err)
	}
	return nil
}
