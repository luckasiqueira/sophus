package repo

import (
	"encoding/json"
	"time"
)

type Message interface {
	Save(connection ConnectionEVO) error
}

type MessageData struct {
	Id               int               `json:"id"`
	MessageId        string            `json:"messageId"`
	Text             string            `json:"text"`
	ConversationId   int               `json:"conversationId"`
	QuotedId         string            `json:"quotedId"`
	MediaType        string            `json:"mediaType"`
	FullJSON         []byte            `json:"fullData"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
	IsFromMe         bool              `json:"isFromMe"`
	IsGroup          bool              `json:"isGroup"`
	IsRead           bool              `json:"isRead"`
	DeliveryStatus   int               `json:"deliveryStatus"`
	IsDeleted        bool              `json:"isDeleted"`
	MediaPath        string            `json:"mediaPath"`
	MediaScope       string            `json:"mediaScope"`
	Sender           string            `json:"sender"`
	SenderName       string            `json:"senderName"`
	SenderNumber     string            `json:"senderNumber"`
	SenderContactId  int               `json:"senderContactId"`
	QuotedText       string            `json:"quotedText"`
	QuotedMediaType  string            `json:"quotedMediaType"`
	QuotedFromMe     bool              `json:"quotedFromMe"`
	QuotedSenderName string            `json:"quotedSenderName"`
	Reactions        []MessageReaction `json:"reactions"`
	Timezone         string            `json:"-"`
	DisplayData      json.RawMessage   `json:"displayData,omitempty"`
	MediaRetryURL    string            `json:"-"`
}

type MessageReaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

type MessageBaseEVO struct {
	Id           string   `json:"id"` //uuid
	Delay        int      `json:"delay"`
	MentionAll   bool     `json:"mentionAll"`
	MentionedJid []string `json:"mentionedJid,omitempty"`
	Number       string   `json:"number"`
	QuotedEVO    `json:"quoted"`
}

type QuotedEVO struct {
	MessageID          string `json:"messageId"`
	Participant        string `json:"participant"`
	MessageParticipant string `json:"messageParticipant,omitempty"`
}

type TextMessageEVO struct {
	MessageBaseEVO
	Text string `json:"text"`
	JSON []byte
}

type ReactionMessageEVO struct {
	Number      string `json:"number"`
	Reaction    string `json:"reaction"`
	ID          string `json:"id"`
	FromMe      bool   `json:"fromMe"`
	Participant string `json:"participant,omitempty"`
}
