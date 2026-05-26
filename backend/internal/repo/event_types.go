package repo

import "github.com/google/uuid"

type EventEVO struct {
	EventType     string    `json:"event"`
	InstanceID    uuid.UUID `json:"instanceId"`
	InstanceName  string    `json:"instanceName"`
	InstanceToken uuid.UUID `json:"instanceToken"`
}

type EventQRCode struct {
	Data struct {
		Code     string `json:"code"`
		QRCode   string `json:"qrcode"`
		Count    int    `json:"count"`
		MaxCount int    `json:"maxCount"`
	} `json:"data"`
	EventEVO
}

type EventMessageEVO struct {
	Data struct {
		Info struct {
			Chat         string `json:"Chat"`
			ID           string `json:"ID"` // can be used by quotes
			IsFromMe     bool   `json:"IsFromMe"`
			IsGroup      bool   `json:"IsGroup"`
			PushName     string `json:"PushName"`
			RecipientAlt string `json:"RecipientAlt"`
			Sender       string `json:"Sender"`    // default
			SenderAlt    string `json:"SenderAlt"` // lid
			Timestamp    string `json:"Timestamp"`
			Type         string `json:"Type"`      // text, media
			Mediatype    string `json:"MediaType"` // "", image, audio, video, document
			IsEdit       bool   `json:"IsEdit"`
			IsEphemeral  bool   `json:"IsEphemeral"`
		} `json:"Info"`
		Message struct {
			Conversation string               `json:"conversation"`
			TXT          EventMessageTextEVO  `json:"extendedTextMessage"`
			IMG          EventMessageImageEVO `json:"imageMessage"`
			VID          EventMessageVideoEVO `json:"videoMessage"`
			Base64       string               `json:"base64"`
		} `json:"Message"`
		IsEphemeral bool `json:"IsEphemeral"`
		IsViewOnce  bool `json:"IsViewOnce"`
		IsEdit      bool `json:"IsEdit"`
	} `json:"data"`
	EventEVO
	FullJSON []byte
}

type EventMessageTextEVO struct {
	Text                string `json:"conversation"`
	ExtendedTextMessage struct {
		Text        string `json:"text"`
		ContextInfo struct {
			QuotedMessageID string `json:"stanzaID"`
			QuotedMessage   struct {
				Text string `json:"conversation"`
			} `json:"quotedMessage"`
		} `json:"contextInfo"`
	}
}

type EventMessageImageEVO struct {
	URL         string `json:"url"`
	ContextInfo struct {
	} `json:"contextInfo"`
	MimeType string `json:"mimetype"`
	Height   int    `json:"height"`
	Width    int    `json:"width"`
	Caption  string `json:"caption"`
}

type EventMessageVideoEVO struct {
	URL         string `json:"url"`
	ContextInfo struct {
	} `json:"contextInfo"`
	MimeType string `json:"mimetype"`
	Seconds  int    `json:"seconds"`
	Height   int    `json:"height"`
	Width    int    `json:"width"`
	Caption  string `json:"caption"`
}
