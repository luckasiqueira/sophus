package repo

import "github.com/google/uuid"

type EventEVO []struct {
	Body struct {
		EventDataEVO
	} `json:"body"`
}

type EventDataEVO struct {
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
		EventDataEVO
	} `json:"body"`
}

type EventMessageEVO []struct {
	Body struct {
		Data struct {
			Info struct {
				Chat        string `json:"Chat"`
				ID          string `json:"ID"` // can be used by quotes
				IsFromMe    bool   `json:"IsFromMe"`
				IsGroup     bool   `json:"IsGroup"`
				PushName    string `json:"PushName"`
				Sender      string `json:"Sender"`    // default
				SenderAlt   string `json:"SenderAlt"` // lid
				Timestamp   string `json:"Timestamp"`
				Type        string `json:"Type"`      // text, media
				Mediatype   string `json:"Mediatype"` // "", image, audio, video, document
				IsEdit      bool   `json:"IsEdit"`
				IsEphemeral bool   `json:"IsEphemeral"`
			} `json:"Info"`
			Message struct {
				Text                string `json:"conversation"`
				ExtendedTextMessage struct {
					ContextInfo struct {
						QuotedMessageID string `json:"stanzaID"`
						QuotedMessage   struct {
							Text string `json:"conversation"`
						} `json:"quotedMessage"`
					} `json:"contextInfo"`
				} `json:"extendedTextMessage"`
			} `json:"Message"`
		} `json:"data"`
		EventDataEVO
	} `json:"body"`
}
