package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sophus/pkg/http/requests"
	"strings"
	"time"
)

var ErrTemporaryAvatarLookup = errors.New("falha temporária ao buscar avatar")
var ErrAvatarNotFound = errors.New("contato sem foto de perfil")

// Evolution GO waits up to 80 seconds for WhatsApp's profile-picture response.
// The client must outlive that deadline to receive the backend's result.
const avatarRequestTimeout = 95 * time.Second

type Contact struct {
	Id        int
	Name      string
	Number    string
	Email     string
	Company   string
	CompanyId int
	JID       string
	LID       string
	IsGroup   bool
	IsBlocked bool
	AvatarURL string
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
	query := `INSERT INTO public.contacts (id, name, number, email, company, "companyId", jid, lid, "isGroup", "isBlocked", "avatarRefreshRequestedAt")
	VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8, $9, now()) RETURNING id;`
	contactId, err := insertInt(query,
		contact.Name,
		contact.Number,
		contact.Email,
		contact.Company,
		contact.CompanyId,
		contact.JID,
		contact.LID,
		contact.IsGroup,
		contact.IsBlocked,
	)
	return contactId, err
}

func IncomingContactExists(companyID int, sender, senderAlt string) (bool, error) {
	number, jid, lid := canonicalWhatsAppIdentity(sender, senderAlt)
	if number == "" && jid == "" && lid == "" {
		return false, nil
	}
	var exists bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM contacts
		WHERE "companyId" = $1 AND COALESCE("isGroup", false) = false
		AND (($2 <> '' AND number = $2) OR ($3 <> '' AND jid = $3) OR ($4 <> '' AND lid = $4)))`,
		companyID, number, jid, lid).Scan(&exists)
	return exists, err
}

func FindOrCreateContact(contact Contact) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if contact.CompanyId <= 0 || (contact.IsGroup && contact.JID == "") ||
		(!contact.IsGroup && contact.Number == "" && contact.JID == "" && contact.LID == "") {
		return 0, errors.New("contato sem identidade canônica")
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, $2)`, contactCompanyLockNamespace, contact.CompanyId); err != nil {
		return 0, err
	}
	var contactID int
	err = tx.QueryRow(`SELECT id FROM contacts WHERE "companyId" = $1 AND (
		($2 AND COALESCE("isGroup", false) = true AND $5 <> '' AND jid = $5)
		OR (NOT $2 AND COALESCE("isGroup", false) = false AND
			(($3 <> '' AND number = $3) OR ($4 <> '' AND lid = $4) OR ($5 <> '' AND jid = $5))))
		ORDER BY CASE WHEN $3 <> '' AND number = $3 THEN 0 ELSE 1 END, id LIMIT 1 FOR UPDATE`,
		contact.CompanyId, contact.IsGroup, contact.Number, contact.LID, contact.JID).Scan(&contactID)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`INSERT INTO contacts
			(name, number, email, company, "companyId", jid, lid, "isGroup", "isBlocked", "avatarRefreshRequestedAt")
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now()) RETURNING id`,
			contact.Name, contact.Number, contact.Email, contact.Company, contact.CompanyId,
			contact.JID, contact.LID, contact.IsGroup, contact.IsBlocked).Scan(&contactID)
	} else if err == nil {
		_, err = tx.Exec(`UPDATE contacts SET
			name = CASE WHEN (BTRIM(name) = '' OR name = 'Contato salvo automaticamente' OR ("isGroup"=true AND name='Grupo do WhatsApp'))
				AND $2::text <> '' AND $2::text <> 'Contato salvo automaticamente' THEN $2::text ELSE name END,
			number = CASE WHEN BTRIM(number) = '' AND $3 <> '' THEN $3 ELSE number END,
			jid = CASE WHEN COALESCE(BTRIM(jid), '') = '' AND $4 <> '' THEN $4 ELSE jid END,
			lid = CASE WHEN COALESCE(BTRIM(lid), '') = '' AND $5 <> '' THEN $5 ELSE lid END
			WHERE id = $1`, contactID, contact.Name, contact.Number, contact.JID, contact.LID)
	}
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return contactID, nil
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
	err = json.Unmarshal(r.Response.Body, &group)
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
	err := db.QueryRow(`SELECT id, name, number, email, company, "companyId", COALESCE(jid, ''), COALESCE(lid, ''),
		COALESCE("isGroup", false), COALESCE("isBlocked", false), COALESCE("avatarUrl", '') FROM contacts WHERE id = $1`, contactId).Scan(
		&contact.Id, &contact.Name, &contact.Number, &contact.Email, &contact.Company, &contact.CompanyId, &contact.JID, &contact.LID, &contact.IsGroup, &contact.IsBlocked, &contact.AvatarURL)
	return contact, err
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
		URL:              ApiBaseURL + "/user/check",
		Method:           "POST",
		Timeout:          5 * time.Second,
		MaxResponseBytes: 64 << 10,
		Payload:          payload,
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
	err = json.Unmarshal(r.Response.Body, &c)
	if err != nil {
		fmt.Println("Unmarshal", err)
		return c, err
	}
	return c, err
}

func GetWhatsAppContactName(number, connectionKey string) (string, error) {
	contacts, err := GetContactEvo(number, connectionKey)
	if err != nil {
		return "", err
	}
	for _, user := range contacts.Data.Users {
		if name := strings.TrimSpace(user.Name); name != "" && name != "Contato salvo automaticamente" {
			return name, nil
		}
	}
	return "", nil
}

func GetWhatsAppAvatar(number, connectionKey string) (string, error) {
	return getWhatsAppAvatar(number, connectionKey)
}

func GetWhatsAppAvatarForConnection(number string, connection ConnectionEVO) (string, error) {
	return getWhatsAppAvatar(number, connection.EvolutionAPIKey())
}

func getWhatsAppAvatar(number, connectionKey string) (string, error) {
	if strings.TrimSpace(number) == "" {
		return "", nil
	}
	_, jid, lid := canonicalWhatsAppIdentity(number)
	if jid == "" {
		jid = lid
	}
	if jid == "" {
		return "", errors.New("número inválido para buscar avatar do WhatsApp")
	}
	// Evolution GO's CreateJID prefixes raw numbers with '+', which is invalid in
	// WhatsApp profile-picture IQs. A canonical JID bypasses that conversion.
	request := requests.Request{
		URL: ApiBaseURL + "/user/avatar", Method: "POST", Timeout: avatarRequestTimeout,
		Payload: map[string]interface{}{"number": jid, "preview": true},
		Headers: map[string]string{"Content-Type": "application/json", "apikey": connectionKey}, Response: requests.Response{},
	}
	if err := request.Do(); err != nil {
		if request.StatusCode != 0 {
			category := avatarBackendFailure(request.StatusCode, request.Response.Body)
			if category == "contato sem foto de perfil" {
				return "", fmt.Errorf("%w: Evolution GO /user/avatar", ErrAvatarNotFound)
			}
			if request.StatusCode == http.StatusTooManyRequests || request.StatusCode >= http.StatusInternalServerError && category != "contato sem foto de perfil" {
				return "", fmt.Errorf("%w: Evolution GO /user/avatar: %s", ErrTemporaryAvatarLookup, category)
			}
			return "", fmt.Errorf("Evolution GO /user/avatar: %s", category)
		}
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) {
			return "", fmt.Errorf("%w: Evolution GO /user/avatar: %v", ErrTemporaryAvatarLookup, err)
		}
		return "", fmt.Errorf("Evolution GO /user/avatar: %w", err)
	}
	if avatarURL := avatarURLFromResponse(request.Response.Body); avatarURL != "" {
		return avatarURL, nil
	}
	return "", fmt.Errorf("%w: Evolution GO /user/avatar respondeu sem URL de avatar", ErrTemporaryAvatarLookup)
}

func avatarBackendFailure(status int, body []byte) string {
	var response struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &response) == nil {
		detail := strings.ToLower(response.Error)
		switch {
		case strings.Contains(detail, "no profile picture found"):
			return "contato sem foto de perfil"
		case strings.Contains(detail, "deadline exceeded"), strings.Contains(detail, "timeout"), strings.Contains(detail, "timed out"):
			return "tempo esgotado na consulta ao WhatsApp"
		case strings.Contains(detail, "client disconnected"), strings.Contains(detail, "no active session"), strings.Contains(detail, "not logged in"):
			return "sessão WhatsApp desconectada"
		case strings.Contains(detail, "invalid phone number"):
			return "número inválido para o WhatsApp"
		}
	}
	return fmt.Sprintf("erro HTTP %d (consulte os logs do Evolution GO)", status)
}

func avatarURLFromResponse(body []byte) string {
	var response struct {
		URL               string          `json:"URL"`
		Url               string          `json:"url"`
		ProfilePictureURL string          `json:"profilePictureUrl"`
		Data              json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	for _, value := range []string{response.URL, response.Url, response.ProfilePictureURL} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	var nested struct {
		URL               string `json:"URL"`
		Url               string `json:"url"`
		ProfilePictureURL string `json:"profilePictureUrl"`
	}
	if json.Unmarshal(response.Data, &nested) == nil {
		for _, value := range []string{nested.URL, nested.Url, nested.ProfilePictureURL} {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}
