package repo

type Contact struct {
	Id           int
	Name         string
	Number       string
	ConnectionId int
	JID          string
	LID          string
	IsGroup      bool
	IsBlocked    bool
}

func CreateContact(contact Contact) (int, error) {
	query := `INSERT INTO public.contacts (id, name, number, "connectionId", jid, lid, "isGroup", "isBlocked")
	VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7) RETURNING id;`
	contactId, err := insertInt(query,
		contact.Name,
		contact.Number,
		contact.ConnectionId,
		contact.JID,
		contact.LID,
		contact.IsGroup,
		contact.IsBlocked,
	)
	return contactId, err
}
