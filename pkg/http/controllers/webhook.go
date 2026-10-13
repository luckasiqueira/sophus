package controllers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sophus/internal/flowengine"
	"sophus/internal/repo"
	"sophus/internal/repo/instances"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
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
		if eventType == "connected" {
			instance := instances.InstanceEVO{InstanceID: connection.InstanceID, APIToken: connection.EvolutionAPIKey()}
			status, statusErr := instance.GetStatus()
			if statusErr != nil {
				log.Printf("failed to verify connected webhook: connection=%d error=%v", connection.Id, statusErr)
				ctx.StopWithStatus(iris.StatusServiceUnavailable)
				return
			}
			if !status.Connected {
				if statusErr := repo.UpdateConnectionStatus(connection.Id, repo.ConnectionStatusDisconnected); statusErr != nil {
					ctx.StopWithStatus(iris.StatusInternalServerError)
					return
				}
				sse.NotifyInstances(connection.CompanyID)
				ctx.StatusCode(iris.StatusOK)
				return
			}
			if number == "" {
				number = status.Number
			}
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
	case "disconnected", "loggedout", "connectfailure", "temporaryban":
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
		if err := enqueueMessageWebhook(ctx.Request().Context(), connection.Id, body); errors.Is(err, errMessageWebhookQueueUnavailable) {
			log.Printf("message webhook queue unavailable; processing synchronously: connection=%d", connection.Id)
			if processErr := processMessageWebhook(connection, body); processErr != nil {
				log.Printf("failed to process Evolution GO webhook synchronously: connection=%d error=%v", connection.Id, processErr)
				ctx.StopWithStatus(iris.StatusInternalServerError)
				return
			}
			ctx.StatusCode(iris.StatusOK)
			return
		} else if err != nil {
			log.Printf("failed to enqueue Evolution GO webhook: connection=%d error=%v", connection.Id, err)
			ctx.StopWithStatus(iris.StatusServiceUnavailable)
			return
		}
		ctx.StatusCode(iris.StatusAccepted)
	case "receipt", "readreceipt":
		if ids, selfRead := repo.ParseSelfReadReceipt(body); selfRead {
			marked, readErr := repo.RecordWhatsAppReads(connection, ids)
			if readErr != nil {
				ctx.StopWithStatus(500)
				return
			}
			if marked > 0 {
				sse.NotifyConversations(connection.CompanyID)
			}
			ctx.StatusCode(200)
			return
		}
		ids, status, parseErr := repo.ParseMessageReceipt(body)
		if parseErr != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
			return
		}
		updates, receiptErr := repo.ApplyMessageReceipt(connection, ids, status)
		if receiptErr != nil {
			log.Printf("message receipt failed: connection=%d error=%v", connection.Id, receiptErr)
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		for _, update := range updates {
			html, renderErr := renderComponent(components.MessageDeliveryStatus(repo.MessageData{MessageId: update.MessageID, DeliveryStatus: update.Status, IsGroup: update.IsGroup}, true))
			if renderErr != nil {
				ctx.StopWithStatus(iris.StatusInternalServerError)
				return
			}
			sse.Global.SendEvent(update.ConversationURL, "message-status", html)
		}
		ctx.StatusCode(iris.StatusOK)
	case "picture":
		var picture repo.EventPictureEVO
		if err := json.Unmarshal(body, &picture); err != nil || strings.TrimSpace(picture.Data.JID) == "" {
			ctx.StopWithStatus(iris.StatusBadRequest)
			return
		}
		ids, err := repo.ApplyContactPictureChange(ctx.Request().Context(), connection.CompanyID, picture.Data.JID, picture.Data.Remove)
		if err != nil {
			log.Printf("contact picture event failed: company=%d connection=%d error=%v", connection.CompanyID, connection.Id, err)
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		for _, id := range ids {
			sse.NotifyContactAvatar(connection.CompanyID, id)
		}
		ctx.StatusCode(iris.StatusOK)
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
			conversation, convErr := repo.GetOpenConversationByContact(connection.Id, click.Data.Phone, click.Data.JID, click.Data.Chat)
			if convErr == nil {
				if conversation.Status == repo.ConversationStatusClosed || conversation.Status == "resolved" {
					next, reopenErr := repo.StartFollowingConversation(conversation.Id)
					if reopenErr != nil {
						ctx.StopWithStatus(500)
						return
					}
					conversation = next
				}
				flowengine.HandleIncomingMessage(conversation, connection, responseID, click.Data.PushName, "interactive", "button:"+click.Data.MessageID)
			}
		}
	}

}

func processMessageWebhook(connection repo.ConnectionEVO, body []byte) error {
	allowed, err := repo.CompanyHasAccess(connection.CompanyID)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("company %d is not allowed to process incoming webhooks", connection.CompanyID)
	}
	var msg repo.EventMessageEVO
	if err := json.Unmarshal(body, &msg); err != nil {
		return err
	}
	msg.FullJSON = body
	repo.NormalizeWebhookMessage(&msg, connection)
	if messageID, handled, err := repo.ApplyWebhookMessageEdit(msg, connection); handled {
		if err != nil || messageID == "" {
			return err
		}
		conversation, err := repo.GetConversationByMessage(messageID, connection.CompanyID, connection.Id)
		if err != nil {
			return err
		}
		stored, err := repo.GetMessageForConversation(messageID, conversation.Id, connection.CompanyID)
		if err != nil {
			return err
		}
		return publishUpdatedWebhookMessage(stored, conversation, connection)
	}
	if repo.IsInternalWhatsAppEvent(msg) || repo.IsNonConversationChat(msg) {
		return nil
	}
	if targetID := msg.Data.Message.Reaction.Key.ID; targetID != "" {
		conversation, conversationErr := repo.GetConversationByMessage(targetID, connection.CompanyID, connection.Id)
		if errors.Is(conversationErr, sql.ErrNoRows) {
			return nil
		}
		if conversationErr != nil {
			return conversationErr
		}
		sender := msg.Data.Info.Sender
		if msg.Data.Info.IsFromMe {
			sender = "self"
		}
		updated, reactionErr := repo.ApplyMessageReaction(targetID, conversation.Id, connection.CompanyID, sender,
			msg.Data.Message.Reaction.Text, msg.Data.Info.ID, msg.Data.Info.IsFromMe)
		if reactionErr != nil {
			return reactionErr
		}
		html, renderErr := renderComponent(components.MessageReactions(updated, conversation.URL.String(), true))
		if renderErr != nil {
			return renderErr
		}
		sse.Global.Send(conversation.URL.String(), html)
		return nil
	}
	exists, err := repo.MessageExists(msg.Data.Info.ID, connection.CompanyID, connection.Id)
	if err != nil {
		return err
	}
	if exists {
		return completeExistingWebhookMedia(msg, connection)
	}
	settings, err := repo.GetCompanyOperationSettings(connection.CompanyID)
	if err != nil {
		return err
	}
	if msg.Data.Info.IsGroup && !settings.AcceptGroupMessages {
		log.Printf("incoming group message ignored by company settings: company=%d message=%s", connection.CompanyID, msg.Data.Info.ID)
		return nil
	}
	if !msg.Data.Info.IsFromMe && !msg.Data.Info.IsGroup && settings.PlatformContactsOnly {
		exists, err := repo.IncomingContactExists(connection.CompanyID, msg.Data.Info.Sender, msg.Data.Info.SenderAlt)
		if err != nil || !exists {
			if err == nil {
				log.Printf("incoming message ignored because contact is not registered: company=%d message=%s", connection.CompanyID, msg.Data.Info.ID)
			}
			return err
		}
	}
	// Publish the message metadata first, then attach the file. Download or
	// storage failures must not hide an action already performed on the phone.
	attachment := false
	switch repo.CheckMessageType(msg) {
	case "image", "video", "audio", "document", "sticker":
		attachment = true
	}
	toStore := msg
	if msg.Data.Info.IsFromMe {
		toStore.PreventConversationCreation = !settings.PhoneConversationCreationEnabled
		toStore.ReopenClosedConversation = settings.PhoneConversationCreationEnabled
	}
	if attachment {
		toStore.Data.Message.Base64 = ""
	}
	if err := repo.SaveEvoMessage(toStore, connection); err != nil {
		if errors.Is(err, repo.ErrPhoneConversationCreationDisabled) {
			log.Printf("phone message skipped because conversation creation is disabled: company=%d connection=%d message=%s", connection.CompanyID, connection.Id, msg.Data.Info.ID)
			return nil
		}
		return err
	}
	if !msg.Data.Info.IsFromMe && !msg.Data.Info.IsGroup {
		conversation, err := repo.GetConversationByMessage(msg.Data.Info.ID, connection.CompanyID, connection.Id)
		if err != nil {
			return err
		}
		if conversation.AgentID != nil {
			if err := prepareSSEData(msg, connection.CompanyID, settings.NotifyNewConversation, settings.Timezone, connection.Id); err != nil {
				return err
			}
			if attachment {
				return completeExistingWebhookMedia(msg, connection)
			}
			return nil
		}
		directAgent, directAgentErr := repo.GetDirectAIAgent(connection.Id, connection.CompanyID)
		if directAgentErr != nil && !errors.Is(directAgentErr, sql.ErrNoRows) {
			log.Printf("failed to check direct AI agent: company=%d connection=%d error=%v", connection.CompanyID, connection.Id, directAgentErr)
		}
		if directAgent.Id > 0 {
			log.Printf("direct AI agent found: company=%d connection=%d conversation=%d agent=%d", connection.CompanyID, connection.Id, conversation.Id, directAgent.Id)
		}
		if shouldAutoAssignConversation(settings.AutoAssignEnabled, directAgent) {
			if err := repo.AutoAssignConversation(conversation.Id, connection.CompanyID, settings.AutoAssignStrategy); err != nil {
				log.Printf("failed to auto-assign conversation: company=%d conversation=%d error=%v", connection.CompanyID, conversation.Id, err)
			}
		}
		inbound := repo.NormalizeFlowInboundMessage(msg)
		flowengine.HandleIncomingMessageWithFlowMatching(conversation, connection, inbound.Text, msg.Data.Info.PushName, inbound.Type, settings.FlowsForAllContacts, "message:"+msg.Data.Info.ID)
		if count, countErr := repo.ConversationMessageCount(conversation.Id); countErr == nil && count == 1 {
			autoReply := strings.TrimSpace(settings.WelcomeMessage)
			if settings.BusinessHoursEnabled && !companyWithinBusinessHours(connection.CompanyID, settings) {
				autoReply = strings.TrimSpace(settings.OutsideHoursMessage)
			}
			if autoReply != "" {
				go sendCompanyPolicyReply(connection, strings.Split(msg.Data.Info.Sender, "@")[0], autoReply)
			}
		}
	}
	if err := prepareSSEData(msg, connection.CompanyID, settings.NotifyNewConversation, settings.Timezone, connection.Id); err != nil {
		return err
	}
	if attachment {
		return completeExistingWebhookMedia(msg, connection)
	}
	return nil
}

func completeExistingWebhookMedia(msg repo.EventMessageEVO, connection repo.ConnectionEVO) error {
	kind := repo.CheckMessageType(msg)
	switch kind {
	case "image", "video", "audio", "document", "sticker":
	default:
		return nil
	}
	conversation, err := repo.GetConversationByMessage(msg.Data.Info.ID, connection.CompanyID, connection.Id)
	if err != nil {
		return err
	}
	stored, err := repo.GetMessageForConversation(msg.Data.Info.ID, conversation.Id, connection.CompanyID)
	if err != nil {
		return err
	}
	if stored.IsDeleted || stored.MediaPath != "" || stored.MediaType == "view_once" {
		return nil
	}
	if err := repo.DownloadWebhookMedia(&msg, connection); err != nil {
		log.Printf("WhatsApp webhook media unavailable: company=%d connection=%d message=%s fromMe=%t type=%s error=%v", connection.CompanyID, connection.Id, msg.Data.Info.ID, msg.Data.Info.IsFromMe, kind, err)
		return err
	}
	updated, err := repo.CompleteWebhookMedia(msg, connection, stored)
	if errors.Is(err, repo.ErrStorageLimitReached) {
		if markErr := repo.MarkWebhookMediaStorageLimit(stored, connection); markErr != nil {
			return markErr
		}
		// A full quota is not a transient provider failure. Keep the descriptor
		// for an explicit retry after storage is freed, rather than retrying the
		// download repeatedly through the webhook queue.
		updated = true
		err = nil
	}
	if err != nil || !updated {
		return err
	}
	stored, err = repo.GetMessageForConversation(msg.Data.Info.ID, conversation.Id, connection.CompanyID)
	if err != nil {
		return err
	}
	return publishUpdatedWebhookMessage(stored, conversation, connection)
}

func publishUpdatedWebhookMessage(stored repo.MessageData, conversation repo.Conversation, connection repo.ConnectionEVO) error {
	html, err := updatedWebhookMessageHTML(stored, conversation, connection)
	if err != nil {
		return err
	}
	sse.Global.SendEvent(conversation.URL.String(), "message-update", html)
	sse.NotifyConversations(connection.CompanyID)
	return nil
}

func updatedWebhookMessageHTML(stored repo.MessageData, conversation repo.Conversation, connection repo.ConnectionEVO) (string, error) {
	settings, err := repo.GetCompanyOperationSettings(connection.CompanyID)
	if err != nil {
		return "", err
	}
	stored.Timezone = settings.Timezone
	if stored.MediaPath == "" {
		messages := []repo.MessageData{stored}
		setMessageMediaRetryURL(messages, conversation.URL.String())
		stored = messages[0]
	}
	var component templ.Component
	if stored.IsFromMe {
		component = components.MessageSent(stored, connection.CompanyID, conversation.URL.String(), true)
	} else {
		component = components.MessageReceived(stored, connection.CompanyID, conversation.Contact.Id, conversation.URL.String(), true)
	}
	return renderComponent(component)
}

func shouldAutoAssignConversation(autoAssignEnabled bool, directAgent repo.AIAgent) bool {
	return autoAssignEnabled && directAgent.Id == 0
}

func companyWithinBusinessHours(companyID int, settings repo.CompanyOperationSettings) bool {
	company, err := repo.GetCompanySettings(companyID)
	if err != nil {
		return true
	}
	location, err := time.LoadLocation(company.Timezone)
	if err != nil {
		return true
	}
	now := time.Now().In(location)
	start, startErr := time.Parse("15:04", settings.BusinessHoursStart)
	end, endErr := time.Parse("15:04", settings.BusinessHoursEnd)
	if startErr != nil || endErr != nil {
		return true
	}
	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes := start.Hour()*60 + start.Minute()
	endMinutes := end.Hour()*60 + end.Minute()
	scheduleDay := int(now.Weekday())
	withinHours := currentMinutes >= startMinutes && currentMinutes < endMinutes
	if startMinutes > endMinutes {
		withinHours = currentMinutes >= startMinutes || currentMinutes < endMinutes
		if currentMinutes < endMinutes {
			scheduleDay = (scheduleDay + 6) % 7
		}
	}
	allowedDay := false
	for _, day := range settings.BusinessHoursDays {
		if day == scheduleDay {
			allowedDay = true
			break
		}
	}
	if !allowedDay || !withinHours {
		return false
	}
	current := now.Format("15:04")
	if settings.BusinessHoursLunchEnabled && current >= settings.BusinessHoursLunchStart && current < settings.BusinessHoursLunchEnd {
		return false
	}
	return true
}

func sendCompanyPolicyReply(connection repo.ConnectionEVO, number, text string) {
	message := repo.TextMessageEVO{MessageBaseEVO: repo.MessageBaseEVO{Number: number, Delay: repo.TypingDelayMilliseconds(text)}, Text: text}
	status, body, err := message.Send(connection.EvolutionAPIKey())
	if err != nil || status != http.StatusOK {
		log.Printf("company policy reply failed: company=%d connection=%d status=%d error=%v", connection.CompanyID, connection.Id, status, err)
		return
	}
	message.JSON = body
	if err := repo.SaveEvoMessage(message, connection); err != nil {
		log.Printf("company policy reply save failed: company=%d connection=%d error=%v", connection.CompanyID, connection.Id, err)
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

func prepareSSEData(msg repo.EventMessageEVO, companyID int, notifyConversations bool, timezone string, connectionIDs ...int) error {
	conversation, err := repo.GetConversationByMessage(msg.Data.Info.ID, companyID, connectionIDs...)
	if err != nil {
		return err
	}
	persisted, err := repo.GetMessageForConversation(msg.Data.Info.ID, conversation.Id, companyID)
	if err != nil {
		return err
	}
	persisted.Timezone = timezone
	if persisted.MediaPath == "" {
		messages := []repo.MessageData{persisted}
		setMessageMediaRetryURL(messages, conversation.URL.String())
		persisted = messages[0]
	}
	msgData := persisted

	var component templ.Component
	if persisted.IsFromMe {
		component = components.MessageSent(msgData, companyID, conversation.URL.String())
	} else {
		component = components.MessageReceived(msgData, companyID, conversation.Contact.Id, conversation.URL.String())
	}

	html, err := renderComponent(component)
	if err != nil {
		return err
	}
	sse.Global.Send(conversation.URL.String(), html)
	sse.NotifyConversations(companyID)
	if notifyConversations {
		if !msg.Data.Info.IsFromMe {
			sse.NotifyIncomingMessage(companyID, conversation.URL.String(), persisted.MessageId)
		}
	}
	return nil
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
