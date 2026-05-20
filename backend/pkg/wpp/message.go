package wpp

type Message interface {
	Save() error
}

type MessageBase struct {
	Id           string   `json:"id"` //uuid
	Delay        int      `json:"delay"`
	MentionAll   bool     `json:"mentionAll"`
	MentionedJid []string `json:"mentionedJid,omitempty"`
	Number       string   `json:"number"`
	Quoted       `json:"quoted"`
}

type Quoted struct {
	MessageID          string `json:"messageId"`
	MessageParticipant string `json:"messageParticipant"`
}
