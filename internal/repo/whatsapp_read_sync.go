package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
	"sophus/pkg/http/requests"
)

type WhatsAppReadBatch struct {
	ConversationID int
	Connection     ConnectionEVO
	Chat           string
	IDs            []string
}

func GetPendingWhatsAppReads(conversationID, companyID int) (WhatsAppReadBatch, error) {
	batch := WhatsAppReadBatch{ConversationID: conversationID}
	var jid, lid, number string
	var group bool
	err := db.QueryRow(`SELECT co.id,co."companyId",COALESCE(co."apiToken",''),co."connectionKey",COALESCE(ct.jid,''),COALESCE(ct.lid,''),ct.number,COALESCE(ct."isGroup",false)
		FROM conversations cv JOIN contacts ct ON ct.id=cv."contactId" AND ct."companyId"=$2 JOIN connections co ON co.id=cv."connectionId" AND co."companyId"=$2
		WHERE cv.id=$1 AND co.status='connected'`, conversationID, companyID).Scan(&batch.Connection.Id, &batch.Connection.CompanyID, &batch.Connection.APIToken, &batch.Connection.ConnectionKey, &jid, &lid, &number, &group)
	if err != nil {
		return batch, err
	}
	if group {
		batch.Chat = jid
	} else {
		batch.Chat = WhatsAppParticipant(jid, lid, number)
	}
	if batch.Chat == "" {
		return batch, nil
	}
	rows, err := db.Query(`SELECT m."messageId" FROM messages m JOIN conversations history ON history.id=m."conversationId" JOIN conversations anchor ON anchor.id=$1
		WHERE history."connectionId"=anchor."connectionId" AND history."contactId"=anchor."contactId" AND (history.id=anchor.id OR history.status='closed')
		AND m."isFromMe"=false AND m."isRead"=true AND m."whatsappReadSynced"=false AND m."isDeleted"=false AND m."messageId"<>'' ORDER BY m.id LIMIT 200`, conversationID)
	if err != nil {
		return batch, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return batch, err
		}
		batch.IDs = append(batch.IDs, id)
	}
	return batch, rows.Err()
}

func SendWhatsAppReadBatch(batch WhatsAppReadBatch) error {
	if len(batch.IDs) == 0 || batch.Chat == "" {
		return nil
	}
	request := requests.Request{URL: ApiBaseURL + "/message/markread", Method: "POST", Payload: map[string]any{"number": batch.Chat, "id": batch.IDs}, Headers: map[string]string{"Content-Type": "application/json", "apikey": batch.Connection.EvolutionAPIKey()}, Timeout: 10 * time.Second, MaxResponseBytes: 512 << 10}
	return request.Do()
}

func RecordWhatsAppReads(connection ConnectionEVO, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO message_read_receipts ("connectionId","messageId") SELECT $1,unnest($2::text[]) ON CONFLICT DO NOTHING`, connection.Id, pq.Array(ids)); err != nil {
		return 0, err
	}
	result, err := tx.Exec(`UPDATE messages m SET "isRead"=true,"whatsappReadSynced"=true FROM conversations cv JOIN connections co ON co.id=cv."connectionId"
		WHERE m."conversationId"=cv.id AND co.id=$1 AND co."companyId"=$2 AND m."isFromMe"=false AND m."messageId"=ANY($3::text[]) AND (m."isRead"=false OR m."whatsappReadSynced"=false)`, connection.Id, connection.CompanyID, pq.Array(ids))
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

func ParseSelfReadReceipt(body []byte) ([]string, bool) {
	var event struct {
		Data struct {
			Type string   `json:"type"`
			IDs  []string `json:"messageIDs"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &event) != nil {
		return nil, false
	}
	switch strings.ToLower(strings.TrimSpace(event.Data.Type)) {
	case "read-self", "played-self":
		return event.Data.IDs, true
	}
	return nil, false
}

var whatsappReadSyncJobs = struct {
	sync.Mutex
	pending map[int]bool
}{pending: map[int]bool{}}
var whatsappReadSyncSlots = make(chan struct{}, 2)

// A failed provider call leaves rows pending for the next view/send. Local read
// state is independent of network latency and no recipient status is forged.
func ScheduleWhatsAppReadSync(conversationID, companyID int) {
	whatsappReadSyncJobs.Lock()
	if _, running := whatsappReadSyncJobs.pending[conversationID]; running {
		whatsappReadSyncJobs.pending[conversationID] = true
		whatsappReadSyncJobs.Unlock()
		return
	}
	whatsappReadSyncJobs.pending[conversationID] = false
	whatsappReadSyncJobs.Unlock()
	go func() {
		whatsappReadSyncSlots <- struct{}{}
		defer func() { <-whatsappReadSyncSlots }()
		for {
			for page := 0; page < 10; page++ {
				batch, err := GetPendingWhatsAppReads(conversationID, companyID)
				if err == nil {
					err = SendWhatsAppReadBatch(batch)
				}
				if err == nil {
					_, err = RecordWhatsAppReads(batch.Connection, batch.IDs)
				}
				if err != nil {
					if !errors.Is(err, sql.ErrNoRows) {
						log.Printf("WhatsApp read sync deferred: company=%d conversation=%d error=%v", companyID, conversationID, err)
					}
					break
				}
				if len(batch.IDs) < 200 {
					break
				}
			}
			whatsappReadSyncJobs.Lock()
			again := whatsappReadSyncJobs.pending[conversationID]
			if !again {
				delete(whatsappReadSyncJobs.pending, conversationID)
			} else {
				whatsappReadSyncJobs.pending[conversationID] = false
			}
			whatsappReadSyncJobs.Unlock()
			if !again {
				return
			}
		}
	}()
}

func markOutgoingConversationRead(conversationID, companyID int) {
	if _, err := MarkConversationRead(conversationID, companyID); err != nil {
		log.Printf("conversation read state after send deferred: company=%d conversation=%d error=%v", companyID, conversationID, err)
	}
}
