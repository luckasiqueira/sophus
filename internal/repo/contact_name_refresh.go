package repo

import (
	"log"
	"strings"
	"sync"
)

var contactNameRefreshSlots = make(chan struct{}, 2)
var contactNameRefreshInFlight sync.Map

// Known/custom names are preserved. Missing names are enriched in bounded,
// deduplicated background work rather than delaying each manual message.
func ScheduleContactNameRefresh(contact Contact, connection ConnectionEVO) {
	name := strings.TrimSpace(contact.Name)
	if contact.Id <= 0 || contact.IsGroup || contact.CompanyId != connection.CompanyID || contact.Number == "" || (name != "" && name != "Contato salvo automaticamente") {
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
