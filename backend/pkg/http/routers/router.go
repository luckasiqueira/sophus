package routers

import (
	"zubly/backend/pkg/http/controllers"
	"zubly/backend/pkg/http/middlewares"

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

}
