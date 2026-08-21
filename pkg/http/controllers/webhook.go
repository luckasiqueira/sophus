package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sophus/internal/flowengine"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/utils"
	"sophus/web/components"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/kataras/iris/v12"
)

var qrConnectionLocks sync.Map

func Webhook(ctx iris.Context) {
	connection, event, body, ok := middlewares.WebhookPayload(ctx)
	if !ok {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	var err error
	saveBody(body)
	eventType := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(event.EventType))
	log.Printf("Evolution GO webhook received: connection=%d event=%s", connection.Id, eventType)
	switch eventType {
	case "qrcode":
		qrcode := repo.EventQRCode{}
		err = json.Unmarshal(body, &qrcode)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
			return
		}
		updated, err := publishQRCode(connection.Id, qrcode.Data.Code, qrcode.Data.QRCode, qrcode.Data.Count, qrcode.Data.MaxCount)
		if err != nil {
			log.Printf("failed to publish QR Code for connection %d: %v", connection.Id, err)
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		if !updated {
			ctx.StatusCode(iris.StatusOK)
			return
		}
		ctx.StatusCode(iris.StatusOK)
	case "pairsuccess", "connected":
		var connected struct {
			Data struct {
				JID    string `json:"jid"`
				Phone  string `json:"phone"`
				Number string `json:"number"`
			} `json:"data"`
		}
		_ = json.Unmarshal(body, &connected)
		number := connected.Data.Number
		if number == "" {
			number = connected.Data.Phone
		}
		if number == "" {
			number = strings.Split(connected.Data.JID, "@")[0]
		}
		lock := qrConnectionLock(connection.Id)
		lock.Lock()
		defer lock.Unlock()
		updated, err := repo.SetConnectionConnected(connection.Id, number)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		if !updated {
			sse.NotifyInstances(connection.CompanyID)
			ctx.StatusCode(iris.StatusOK)
			return
		}
		key := strconv.Itoa(connection.Id)
		sse.QRGlobal.Clear(key)
		payload, _ := json.Marshal(iris.Map{"status": "connected", "number": number})
		sse.QRGlobal.SendEvent(key, "connection", string(payload))
		sse.NotifyInstances(connection.CompanyID)
		ctx.StatusCode(iris.StatusOK)
	case "qrtimeout":
		lock := qrConnectionLock(connection.Id)
		lock.Lock()
		defer lock.Unlock()
		updated, err := repo.MarkConnectionQRTimeout(connection.Id)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		if !updated {
			ctx.StatusCode(iris.StatusOK)
			return
		}
		key := strconv.Itoa(connection.Id)
		sse.QRGlobal.Clear(key)
		payload, _ := json.Marshal(iris.Map{"status": eventType})
		sse.QRGlobal.SendEvent(key, "connection", string(payload))
		sse.NotifyInstances(connection.CompanyID)
		ctx.StatusCode(iris.StatusOK)
	case "disconnected", "loggedout":
		lock := qrConnectionLock(connection.Id)
		lock.Lock()
		defer lock.Unlock()
		if err := repo.UpdateConnectionStatus(connection.Id, "disconnected"); err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		key := strconv.Itoa(connection.Id)
		sse.QRGlobal.Clear(key)
		payload, _ := json.Marshal(iris.Map{"status": eventType})
		sse.QRGlobal.SendEvent(key, "connection", string(payload))
		sse.NotifyInstances(connection.CompanyID)
		ctx.StatusCode(iris.StatusOK)
	case "message":
		if !companyCanProcessWebhook(ctx, connection.CompanyID) {
			return
		}
		msg := repo.EventMessageEVO{}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
			return
		}
		msg.FullJSON = body
		err = repo.SaveEvoMessage(msg, connection)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		if !msg.Data.Info.IsFromMe && !msg.Data.Info.IsGroup {
			conversation, convErr := repo.GetConversationByMessage(msg.Data.Info.ID)
			if convErr == nil {
				messageText := repo.CheckMessageText(msg)
				log.Printf("dispatching inbound message to flows: connection=%d conversation=%d status=%s text_length=%d", connection.Id, conversation.Id, conversation.Status, len(strings.TrimSpace(messageText)))
				flowengine.HandleIncomingMessage(conversation, connection, messageText, msg.Data.Info.PushName, repo.CheckFlowMessageType(msg))
			} else {
				log.Printf("failed to find conversation for flow dispatch: connection=%d message=%s error=%v", connection.Id, msg.Data.Info.ID, convErr)
			}
		}
		prepareSSEData(ctx, msg, connection.CompanyID)
	case "buttonclick":
		if !companyCanProcessWebhook(ctx, connection.CompanyID) {
			return
		}
		click := repo.EventButtonClickEVO{}
		if err = json.Unmarshal(body, &click); err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
			return
		}
		responseID := click.Data.ButtonID
		if responseID == "" {
			responseID = click.Data.RowID
		}
		if !click.Data.FromMe && responseID != "" {
			conversation, convErr := repo.GetOpenConversationByContact(connection.Id, click.Data.Phone, click.Data.JID)
			if convErr == nil {
				if conversation.Status == repo.ConversationStatusClosed || conversation.Status == "resolved" {
					if reopenErr := repo.ReopenConversation(conversation.Id); reopenErr == nil {
						conversation.Status = repo.ConversationStatusOpen
					}
				}
				flowengine.HandleIncomingMessage(conversation, connection, responseID, click.Data.PushName, "interactive")
			}
		}
	}

}

func companyCanProcessWebhook(ctx iris.Context, companyID int) bool {
	allowed, err := repo.CompanyHasAccess(companyID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return false
	}
	if !allowed {
		ctx.StatusCode(iris.StatusNoContent)
		return false
	}
	return true
}

func qrCodeLifetime(count int) time.Duration {
	if count <= 1 {
		return 60 * time.Second
	}
	return 20 * time.Second
}

func publishQRCode(connectionID int, code, image string, count, maxCount int) (bool, error) {
	lock := qrConnectionLock(connectionID)
	lock.Lock()
	defer lock.Unlock()
	if strings.HasPrefix(code, "data:image/") {
		code, image = image, code
	}
	if image == "" {
		return false, fmt.Errorf("QR Code image is empty")
	}
	updated, err := repo.UpdateConnectionQRCode(connectionID, image)
	if err != nil || !updated {
		return updated, err
	}
	payload, err := json.Marshal(iris.Map{
		"code":      code,
		"qrcode":    image,
		"count":     count,
		"maxCount":  maxCount,
		"expiresAt": time.Now().Add(qrCodeLifetime(count)).UnixMilli(),
	})
	if err != nil {
		return false, err
	}
	sse.QRGlobal.SendEvent(strconv.Itoa(connectionID), "qrcode", string(payload))
	return true, nil
}

func qrConnectionLock(connectionID int) *sync.Mutex {
	lock, _ := qrConnectionLocks.LoadOrStore(connectionID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func prepareSSEData(ctx iris.Context, msg repo.EventMessageEVO, companyID int) {
	//u := ctx.URLParam("url")
	//url, err := uuid.Parse(u)
	msgText := repo.CheckMessageText(msg)
	msgType := repo.CheckMessageType(msg)
	t, err := utils.TimeFromTimestamp(msg.Data.Info.Timestamp)
	if err != nil {
		log.Printf("invalid message timestamp: message=%s timestamp=%q error=%v", msg.Data.Info.ID, msg.Data.Info.Timestamp, err)
	}
	msgData := repo.MessageData{
		MessageId:      msg.Data.Info.ID,
		Text:           msgText,
		ConversationId: 0,
		MediaType:      msgType,
		CreatedAt:      t,
		UpdatedAt:      t,
		IsFromMe:       msg.Data.Info.IsFromMe,
		IsGroup:        msg.Data.Info.IsGroup,
		IsDeleted:      false,
		MediaPath:      msg.MediaPath,
	}

	conversation, err := repo.GetConversationByMessage(msg.Data.Info.ID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}

	var component templ.Component
	if msgType == "text" {
		msgData.QuotedId = msg.Data.Message.TXT.ContextInfo.QuotedMessageID
	}
	if msgType == "image" || msgType == "video" || msgType == "audio" || msgType == "document" {
		msgData.MediaPath, err = repo.GetMediaPathByMessage(msgData.MessageId)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
	}
	if msg.Data.Info.IsFromMe {
		component = components.MessageSent(msgData, companyID)
	} else {
		component = components.MessageReceived(msgData, companyID)
	}

	html, err := renderComponent(component)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		return
	}
	sse.Global.Send(conversation.URL.String(), html)
	sse.NotifyConversations(companyID)
	ctx.StatusCode(iris.StatusOK)
}

func renderComponent(c templ.Component) (string, error) {
	var buf bytes.Buffer
	err := c.Render(context.Background(), &buf)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// adjust to save json onto a separate table messages_body
func saveBody(body []byte) {
	file := fmt.Sprintf("%s.txt", time.Now().Format("20060102150405"))
	if err := os.WriteFile(file, body, os.ModePerm); err != nil {
		fmt.Println(err)
	}
}
