package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sophus/internal/flowengine"
	"sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/utils"
	"sophus/web/components"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/kataras/iris/v12"
)

func Webhook(ctx iris.Context) {
	connection, event, body, err := middlewares.ValidateWebhook(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	saveBody(body)
	eventType := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(event.EventType))
	switch eventType {
	case "qrcode":
		qrcode := repo.EventQRCode{}
		err = json.Unmarshal(body, &qrcode)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
			return
		}
		updated, err := repo.UpdateConnectionQRCode(connection.Id, qrcode.Data.QRCode)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		if !updated {
			ctx.StatusCode(iris.StatusOK)
			return
		}
		payload, _ := json.Marshal(iris.Map{
			"code":      qrcode.Data.Code,
			"qrcode":    qrcode.Data.QRCode,
			"count":     qrcode.Data.Count,
			"maxCount":  qrcode.Data.MaxCount,
			"expiresAt": time.Now().Add(qrCodeLifetime(qrcode.Data.Count)).UnixMilli(),
		})
		sse.QRGlobal.SendEvent(strconv.Itoa(connection.Id), "qrcode", string(payload))
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
		updated, err := repo.SetConnectionConnected(connection.Id, number)
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
		payload, _ := json.Marshal(iris.Map{"status": "connected", "number": number})
		sse.QRGlobal.SendEvent(key, "connection", string(payload))
		ctx.StatusCode(iris.StatusOK)
	case "qrtimeout":
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
		ctx.StatusCode(iris.StatusOK)
	case "disconnected", "loggedout":
		if err := repo.UpdateConnectionStatus(connection.Id, "disconnected"); err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		key := strconv.Itoa(connection.Id)
		sse.QRGlobal.Clear(key)
		payload, _ := json.Marshal(iris.Map{"status": eventType})
		sse.QRGlobal.SendEvent(key, "connection", string(payload))
		ctx.StatusCode(iris.StatusOK)
	case "message":
		msg := repo.EventMessageEVO{}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
			return
		}
		msg.FullJSON = body
		err = repo.SaveEvoMessage(msg, connection)
		fmt.Println(err)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
			return
		}
		if !msg.Data.Info.IsFromMe && !msg.Data.Info.IsGroup {
			conversation, convErr := repo.GetConversationByMessage(msg.Data.Info.ID)
			if convErr == nil {
				messageText := repo.CheckMessageText(msg)
				flowengine.HandleIncomingMessage(conversation, connection, messageText, msg.Data.Info.PushName)
			}
		}
		prepareSSEData(ctx, msg)
	case "buttonclick":
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
				flowengine.HandleIncomingMessage(conversation, connection, responseID, click.Data.PushName)
			}
		}
	}

}

func qrCodeLifetime(count int) time.Duration {
	if count <= 1 {
		return 60 * time.Second
	}
	return 20 * time.Second
}

func prepareSSEData(ctx iris.Context, msg repo.EventMessageEVO) {
	//u := ctx.URLParam("url")
	//url, err := uuid.Parse(u)
	msgText := repo.CheckMessageText(msg)
	msgType := repo.CheckMessageType(msg)
	t, err := utils.TimeFromTimestamp(msg.Data.Info.Timestamp)
	fmt.Println(t, err)
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

	agent, err := repo.GetAgentByMessage(msg.Data.Info.ID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
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
		component = components.MessageSent(msgData, agent.CompanyId)
	} else {
		component = components.MessageReceived(msgData, agent.CompanyId)
	}

	html, err := renderComponent(component)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		return
	}
	sse.Global.Send(conversation.URL.String(), html)
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
