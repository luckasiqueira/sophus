package routers

import (
	"sophus/backend/internal/repo"
	"sophus/backend/pkg/http/controllers"
	"sophus/backend/pkg/http/middlewares"
	"sophus/backend/web"

	"github.com/google/uuid"
	"github.com/kataras/iris/v12"
)

func Router(r *iris.Application) {
	r.Get("/login", controllers.Login)
	r.Post("/dologin", controllers.DoLogin)

	r.Post("/webhook/{webhookId:uuid}", controllers.Webhook)

	api := r.Party("/api")
	api.Use(middlewares.AuthAPI)
	{
		message := api.Party("/message")
		{
			message.Post("/send", controllers.SendMessage)
		}

	}

	r.Use(middlewares.AuthLogin)
	r.Get("/messages", func(ctx iris.Context) {
		agent, err := middlewares.AgentIdentifier(ctx)
		if err != nil {
			ctx.StopWithStatus(iris.StatusUnauthorized)
		}
		conversations, err := repo.GetConversationsByAgent(agent)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
		ctx.RenderComponent(web.Messages(conversations))
	})

	r.Get("/messages/{url:uuid}", func(ctx iris.Context) {
		agent, err := middlewares.AgentIdentifier(ctx)
		if err != nil {
			ctx.StopWithStatus(iris.StatusUnauthorized)
		}
		conversations, err := repo.GetConversationsByAgent(agent)
		if err != nil {
			ctx.StopWithStatus(iris.StatusInternalServerError)
		}
		u := ctx.Params().Get("url")
		url, err := uuid.Parse(u)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		messages, err := repo.GetMessagesByConversationURL(url)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		activeConversation := repo.Conversation{}
		for _, c := range conversations {
			if c.URL == url {
				activeConversation = c
				break
			}
		}
		activeConversation.Contact, err = repo.GetContactById(activeConversation.Contact.Id)
		if err != nil {
			ctx.StopWithStatus(iris.StatusBadRequest)
		}
		ctx.RenderComponent(web.MessageSingle(activeConversation, conversations, messages))

	})

	//instance := r.Party("/instance")
	//instance.Use(middlewares.IsValidAPIToken)
	//{
	//	instance.Post("/create", controllers.NewInstance)
	//}

}
