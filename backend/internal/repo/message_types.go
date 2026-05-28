package repo

import "time"

type Message interface {
	Save(connection ConnectionEVO) error
}

type MessageData struct {
	Id             int       `json:"id"`
	MessageId      string    `json:"messageId"`
	Text           string    `json:"text"`
	ConversationId int       `json:"conversationId"`
	QuotedId       string    `json:"quotedId"`
	MediaType      string    `json:"mediaType"`
	FullJSON       []byte    `json:"fullData"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	IsFromMe       bool      `json:"isFromMe"`
	IsGroup        bool      `json:"isGroup"`
	IsRead         bool      `json:"isRead"`
	IsDeleted      bool      `json:"isDeleted"`
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
	MessageParticipant string `json:"messageParticipant"`
}

type TextMessageEVO struct {
	MessageBaseEVO
	Text string `json:"text"`
	JSON []byte
}
