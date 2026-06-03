package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	repo2 "sophus/internal/repo"
	"sophus/pkg/http/middlewares"
	"sophus/pkg/http/middlewares/sse"
	"sophus/utils"
	"sophus/web/components"
	"time"

	"github.com/a-h/templ"
	"github.com/kataras/iris/v12"
)

func Webhook(ctx iris.Context) {
	connection, event, body, err := middlewares.ValidateWebhook(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
	}
	saveBody(body)
	switch event.EventType {
	case "QRCode":
		qrcode := repo2.EventQRCode{}
		err = json.Unmarshal(body, &qrcode)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
	case "Message":
		msg := repo2.EventMessageEVO{}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		msg.FullJSON = body
		err = repo2.SaveEvoMessage(msg, connection)
		fmt.Println(err)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
		prepareSSEData(ctx, msg)
	}

}

func prepareSSEData(ctx iris.Context, msg repo2.EventMessageEVO) {
	//u := ctx.URLParam("url")
	//url, err := uuid.Parse(u)
	msgText := repo2.CheckMessageText(msg)
	msgType := repo2.CheckMessageType(msg)
	t, err := utils.TimeFromTimestamp(msg.Data.Info.Timestamp)
	fmt.Println(t, err)
	msgData := repo2.MessageData{
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

	agent, err := repo2.GetAgentByMessage(msg.Data.Info.ID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}
	conversation, err := repo2.GetConversationByMessage(msg.Data.Info.ID)
	if err != nil {
		ctx.StopWithStatus(iris.StatusInternalServerError)
		return
	}

	var component templ.Component
	switch msgType {
	case "text":
		msgData.QuotedId = msg.Data.Message.TXT.ContextInfo.QuotedMessageID
	case "image":
		msgData.MediaPath, err = repo2.GetMediaPathByMessage(msgData.MessageId)
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
