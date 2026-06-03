package repo

import (
	"encoding/json"
	"fmt"
	"sophus/pkg/http/requests"
)

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

type ContactEVO struct {
	Data struct {
		Users []struct {
			Name      string
			JID       string `json:"JID"`
			RemoteJID string `json:"RemoteJID"`
			LID       string `json:"LID"`
		} `json:"users"`
	} `json:"data"`
}

type ContactGroupEVO struct {
	Data struct {
		JID  string `json:"JID"`
		Name string `json:"Name"`
	} `json:"data"`
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

func getGroupInfo(jid, connectionKey string) (Contact, error) {
	var contact Contact
	type g struct {
		GroupJID string
	}
	payload := g{
		GroupJID: jid,
	}
	r := requests.Request{
		URL:     ApiBaseURL + "/group/info",
		Payload: payload,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       connectionKey,
		},
		Method:   "POST",
		Response: requests.Response{},
	}
	err := r.Do()
	if err != nil {
		return contact, err
	}
	group := ContactGroupEVO{}
	err = json.Unmarshal(r.Body, &group)
	if err != nil {
		return contact, err
	}
	contact.Name = group.Data.Name
	contact.JID = group.Data.JID
	contact.IsGroup = true
	return contact, nil
}

func GetContactById(contactId int) (Contact, error) {
	var contact Contact
	stmt, err := db.Prepare(`SELECT * FROM contacts WHERE id= $1;`)
	if err != nil {
		return contact, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(contactId).Scan(&contact.Id, &contact.Name, &contact.Number, &contact.ConnectionId, &contact.JID, &contact.LID, &contact.IsGroup, &contact.IsBlocked)
	return contact, nil
}

func GetContactEvo(number, connectionKey string) (ContactEVO, error) {
	c := ContactEVO{}
	type p struct {
		Number string
	}
	payload := p{
		Number: number,
	}
	r := requests.Request{
		URL:     ApiBaseURL + "/user/check",
		Method:  "POST",
		Payload: payload,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       connectionKey,
		},
		Response: requests.Response{},
	}
	err := r.Do()
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(r.Body, &c)
	if err != nil {
		fmt.Println("Unmarshal", err)
		return c, err
	}
	return c, err
}
