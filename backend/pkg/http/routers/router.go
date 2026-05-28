package routers

import (
	"sophus/backend/pkg/http/controllers"
	"sophus/backend/pkg/http/middlewares"
	"sophus/backend/web"

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
	r.Get("/", func(ctx iris.Context) {
		ctx.RenderComponent(web.Home())
	})

	//instance := r.Party("/instance")
	//instance.Use(middlewares.IsValidAPIToken)
	//{
	//	instance.Post("/create", controllers.NewInstance)
	//}

}
