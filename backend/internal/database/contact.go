package database

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
	stmt, err := db.Prepare(`INSERT INTO public.contacts (id, name, number, "connectionId", jid, lid, "isGroup", "isBlocked")
	VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7) RETURNING id;`)
	if err != nil {
		return 0, err
	}
	var contactId int
	err = stmt.QueryRow(
		contact.Name,
		contact.Number,
		contact.ConnectionId,
		contact.JID,
		contact.LID,
		contact.IsGroup,
		contact.IsBlocked,
	).Scan(&contactId)
	if err != nil {
		return 0, err
	}
	return contactId, nil
}
