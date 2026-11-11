package repo

import (
	"log"
	"strings"
	"sync"
)

// PlaceholderContactName marks a contact whose WhatsApp name is still unknown
// (outgoing messages carry the connection's own push name, not the contact's).
const PlaceholderContactName = "Contato salvo automaticamente"

func isPlaceholderContactName(name string) bool {
	name = strings.TrimSpace(name)
	return name == "" || name == PlaceholderContactName
}

var contactNameRefreshSlots = make(chan struct{}, 2)
var contactNameRefreshInFlight sync.Map

// Known/custom names are preserved. Missing names are enriched in bounded,
// deduplicated background work rather than delaying each manual message.
func ScheduleContactNameRefresh(contact Contact, connection ConnectionEVO) {
	if contact.Id <= 0 || contact.IsGroup || contact.CompanyId != connection.CompanyID || contact.Number == "" || !isPlaceholderContactName(contact.Name) {
		return
	}
	key := [2]int{contact.CompanyId, contact.Id}
	if _, exists := contactNameRefreshInFlight.LoadOrStore(key, struct{}{}); exists {
		return
	}
	select {
	case contactNameRefreshSlots <- struct{}{}:
	default:
		contactNameRefreshInFlight.Delete(key)
		return
	}
	database := db
	go func() {
		defer func() { <-contactNameRefreshSlots; contactNameRefreshInFlight.Delete(key) }()
		updated, err := GetWhatsAppContactName(contact.Number, connection.EvolutionAPIKey())
		if err != nil || updated == "" {
			return
		}
		// Do not overwrite a manual edit made while the remote lookup ran.
		_, err = database.Exec(`UPDATE contacts SET name = $1 WHERE id = $2 AND "companyId" = $3
			AND name = $4 AND number = $5`, updated, contact.Id, contact.CompanyId, contact.Name, contact.Number)
		if err != nil {
			log.Printf("contact name refresh failed: company=%d contact=%d error=%v", contact.CompanyId, contact.Id, err)
		}
	}()
}

// scheduleConversationContactNameRefresh resolves the stored contact behind a
// conversation and enriches it when it still carries the placeholder name. It
// covers contacts first seen through outgoing messages (phone, API, flows),
// which would otherwise keep the placeholder until the contact replied.
func scheduleConversationContactNameRefresh(conversationID int, connection ConnectionEVO) {
	if conversationID <= 0 || connection.CompanyID <= 0 {
		return
	}
	var contact Contact
	err := db.QueryRow(`SELECT ct.id, ct.name, COALESCE(ct.number, ''), ct."companyId", COALESCE(ct."isGroup", false)
		FROM conversations cv JOIN contacts ct ON ct.id = cv."contactId"
		WHERE cv.id = $1 AND ct."companyId" = $2`, conversationID, connection.CompanyID).Scan(
		&contact.Id, &contact.Name, &contact.Number, &contact.CompanyId, &contact.IsGroup)
	if err != nil {
		return
	}
	ScheduleContactNameRefresh(contact, connection)
}
