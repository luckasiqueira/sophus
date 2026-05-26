package routers

import (
	"sophus/backend/pkg/http/controllers"
	"sophus/backend/pkg/http/middlewares"

	"github.com/kataras/iris/v12"
)

func Router(r *iris.Application) {
	r.Any("/", func(ctx iris.Context) {
		ctx.HTML("<h1>Hello World</h1>")
	})

	r.Post("/webhook/{webhookId:uuid}", controllers.Webhook)
	message := r.Party("/message")
	message.Use(middlewares.IsValidAPIToken)
	{
		message.Post("/send", controllers.SendMessage)
	}
	//instance := r.Party("/instance")
	//instance.Use(middlewares.IsValidAPIToken)
	//{
	//	instance.Post("/create", controllers.NewInstance)
	//}

}
