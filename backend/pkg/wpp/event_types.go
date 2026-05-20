package wpp

import "github.com/google/uuid"

type Event []struct {
	Body struct {
		EventData
	} `json:"body"`
}

type EventData struct {
	EventType     string    `json:"event"`
	InstanceID    uuid.UUID `json:"instanceId"`
	InstanceName  string    `json:"instanceName"`
	InstanceToken uuid.UUID `json:"instanceToken"`
}

type EventQRCode []struct {
	Body struct {
		Data struct {
			Code     string `json:"code"`
			QRCode   string `json:"qrcode"`
			Count    int    `json:"count"`
			MaxCount int    `json:"maxCount"`
		} `json:"data"`
		EventData
	} `json:"body"`
}
