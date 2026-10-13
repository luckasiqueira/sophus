package repo

import "github.com/google/uuid"
import "encoding/json"

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

// Evolution GO forwards whatsmeow's Picture event without a photo URL.
// JID identifies the contact; Remove indicates an explicitly deleted photo.
type EventPictureEVO struct {
	Data struct {
		JID    string `json:"jid"`
		Remove bool   `json:"remove"`
	} `json:"data"`
	EventEVO
}

type EventButtonClickEVO struct {
	Data struct {
		ButtonID   string `json:"buttonId"`
		RowID      string `json:"rowId"`
		ButtonText string `json:"buttonText"`
		Type       string `json:"type"`
		Phone      string `json:"phone"`
		JID        string `json:"jid"`
		PushName   string `json:"pushName"`
		MessageID  string `json:"messageId"`
		Chat       string `json:"chat"`
		FromMe     bool   `json:"fromMe"`
		Timestamp  int64  `json:"timestamp"`
	} `json:"data"`
	EventEVO
}

type EventMessageEVO struct {
	Data struct {
		Key struct {
			FromMe    bool   `json:"fromMe"`
			ID        string `json:"id"`
			RemoteJID string `json:"remoteJid"`
		} `json:"key"`
		Info struct {
			Chat           string `json:"Chat"`
			ID             string `json:"ID"` // can be used by quotes
			IsFromMe       bool   `json:"IsFromMe"`
			IsGroup        bool   `json:"IsGroup"`
			PushName       string `json:"PushName"`
			RecipientAlt   string `json:"RecipientAlt"`
			Sender         string `json:"Sender"`    // default
			SenderAlt      string `json:"SenderAlt"` // lid
			Timestamp      string `json:"Timestamp"`
			Type           string `json:"Type"`      // text, media
			Mediatype      string `json:"MediaType"` // "", image, audio, video, document
			IsEdit         bool   `json:"IsEdit"`
			IsEphemeral    bool   `json:"IsEphemeral"`
			DeviceSentMeta *struct {
				DestinationJID string `json:"DestinationJID"`
			} `json:"DeviceSentMeta"`
		} `json:"Info"`
		Message struct {
			Key struct {
				FromMe    bool   `json:"fromMe"`
				ID        string `json:"id"`
				RemoteJID string `json:"remoteJid"`
			} `json:"key"`
			Text         string                  `json:"text"`
			Conversation string                  `json:"conversation"`
			TXT          EventMessageTextEVO     `json:"extendedTextMessage"`
			IMG          EventMessageImageEVO    `json:"imageMessage"`
			VID          EventMessageVideoEVO    `json:"videoMessage"`
			AUD          EventMessageAudioEVO    `json:"audioMessage"`
			DOC          EventMessageDocumentEVO `json:"documentMessage"`
			STK          EventMessageStickerEVO  `json:"stickerMessage"`
			Reaction     EventReactionMessageEVO `json:"reactionMessage"`
			Base64       string                  `json:"base64"`
		} `json:"Message"`
		IsEphemeral bool            `json:"IsEphemeral"`
		IsViewOnce  bool            `json:"IsViewOnce"`
		IsEdit      bool            `json:"IsEdit"`
		GroupData   json.RawMessage `json:"groupData"`
	} `json:"data"`
	EventEVO
	FullJSON                    []byte
	MediaPath                   string `json:"mediaPath"`
	PreventConversationCreation bool   `json:"-"`
	ReopenClosedConversation    bool   `json:"-"`
}

type EventMessageTextEVO struct {
	Text        string              `json:"text"`
	ContextInfo EventContextInfoEVO `json:"contextInfo"`
}

type EventContextInfoEVO struct {
	QuotedMessageID string `json:"stanzaID"`
	Participant     string `json:"participant"`
}

type EventMessageImageEVO struct {
	URL         string              `json:"url"`
	ContextInfo EventContextInfoEVO `json:"contextInfo"`
	MimeType    string              `json:"mimetype"`
	Height      int                 `json:"height"`
	Width       int                 `json:"width"`
	Caption     string              `json:"caption"`
}

type EventMessageVideoEVO struct {
	URL         string              `json:"url"`
	ContextInfo EventContextInfoEVO `json:"contextInfo"`
	MimeType    string              `json:"mimetype"`
	Seconds     int                 `json:"seconds"`
	Height      int                 `json:"height"`
	Width       int                 `json:"width"`
	Caption     string              `json:"caption"`
}

type EventMessageAudioEVO struct {
	URL         string              `json:"url"`
	ContextInfo EventContextInfoEVO `json:"contextInfo"`
	MimeType    string              `json:"mimetype"`
	Seconds     int                 `json:"seconds"`
	Height      int                 `json:"height"`
	Width       int                 `json:"width"`
	Caption     string              `json:"caption"`
}

type EventMessageDocumentEVO struct {
	URL         string              `json:"url"`
	MimeType    string              `json:"mimetype"`
	FileName    string              `json:"fileName"`
	Title       string              `json:"title"`
	Caption     string              `json:"caption"`
	ContextInfo EventContextInfoEVO `json:"contextInfo"`
}

type EventMessageStickerEVO struct {
	URL         string              `json:"url"`
	MimeType    string              `json:"mimetype"`
	IsAnimated  bool                `json:"isAnimated"`
	ContextInfo EventContextInfoEVO `json:"contextInfo"`
}

type EventReactionMessageEVO struct {
	Key struct {
		RemoteJID   string `json:"remoteJID"`
		FromMe      bool   `json:"fromMe"`
		ID          string `json:"ID"`
		Participant string `json:"participant"`
	} `json:"key"`
	Text string `json:"text"`
}
