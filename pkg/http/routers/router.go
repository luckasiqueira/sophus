package routers

import (
	"sophus/pkg/http/controllers"
	"sophus/pkg/http/middlewares"

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
	r.Get("/sse", middlewares.SSEHandler)
	r.Get("/messages", controllers.Messages)

	r.Get("/messages/{url:uuid}", controllers.MessageOpen)

	//instance := r.Party("/instance")
	//instance.Use(middlewares.IsValidAPIToken)
	//{
	//	instance.Post("/create", controllers.NewInstance)
	//}

}
