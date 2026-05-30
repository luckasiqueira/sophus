package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sophus/backend/internal/repo"
	"sophus/backend/pkg/http/middlewares"
	"sophus/backend/pkg/http/middlewares/sse"
	"sophus/backend/utils"
	"sophus/backend/web/components"
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
	agent, err := middlewares.AgentIdentifier(ctx)
	if err != nil {
		ctx.StopWithStatus(iris.StatusBadRequest)
		return
	}
	switch event.EventType {
	case "QRCode":
		qrcode := repo.EventQRCode{}
		err = json.Unmarshal(body, &qrcode)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
	case "Message":
		msg := repo.EventMessageEVO{}
		err = json.Unmarshal(body, &msg)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		msg.FullJSON = body
		err = repo.SaveEvoMessage(msg, connection)
		fmt.Println(err)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
		prepareSSEData(agent, ctx, msg)
	}

}

func prepareSSEData(agent repo.Agent, ctx iris.Context, msg repo.EventMessageEVO) {
	msgText := repo.CheckMessageText(msg)
	msgType := repo.CheckMessageType(msg)
	t, _ := utils.TimeFromTimestamp(msg.Data.Info.Timestamp)
	msgData := repo.MessageData{
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
	var component templ.Component
	switch msgType {
	case "text":
		msgData.QuotedId = msg.Data.Message.TXT.ContextInfo.QuotedMessageID
		if msg.Data.Info.IsFromMe {
			component = components.MessageSent(msgData, agent.CompanyId)

		} else {
			component = components.MessageReceived(msgData, agent.CompanyId)
		}
	}

	html, err := renderComponent(component)
	if err != nil {
		ctx.StatusCode(iris.StatusInternalServerError)
		return
	}
	sse.Global.Send(agent.Id, html)
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
