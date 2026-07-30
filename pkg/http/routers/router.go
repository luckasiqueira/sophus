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

	r.Get("/sse", middlewares.SSEMessages)

	r.Get("/messages", controllers.Messages)
	r.Get("/messages/{url:uuid}", controllers.MessageOpen)

	flows := r.Party("/flows")
	{
		// HTML page (the visual builder SPA)
		flows.Get("/", controllers.FlowBuilderPage)
		flows.Get("/builder", controllers.FlowBuilderPage)

		// JSON API consumed by that page's JS - kept under its own /api
		// sub-path so it never collides with the page route above.
		flowsAPI := flows.Party("/api")
		{
			flowsAPI.Get("/", controllers.ListFlows)
			flowsAPI.Get("/connections", controllers.ListConnectionsForFlows)
			flowsAPI.Get("/{id:int}", controllers.GetFlow)
			flowsAPI.Post("/", controllers.CreateFlow)
			flowsAPI.Put("/{id:int}", controllers.UpdateFlow)
			flowsAPI.Delete("/{id:int}", controllers.DeleteFlow)
			flowsAPI.Post("/{id:int}/execute", controllers.ExecuteFlowTest)
		}
	}

	instance := r.Party("/instances")
	{
		instance.Get("/", controllers.Instances)
		instance.Get("/create", controllers.InstancePopup)
		instance.Post("/create", controllers.NewInstance)
		instance.Get("/{id:int}/qrcode/events", middlewares.SSEQRCode)
	}

}
