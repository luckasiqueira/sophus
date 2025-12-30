package database

import (
	"fmt"
)

type Message struct {
	ChatId    int
	CompanyId int
	Content   string
}

func (m *Message) incoming() error {
	query := `INSERT INTO public.messages (id, content) VALUES (DEFAULT, $1);`
	return insert(query, m.Content)
}

func MessageIncoming() {
	m := Message{
		Content: "Hello World!",
	}
	err := m.incoming()
	if err != nil {
		fmt.Println(err)
	}
}
