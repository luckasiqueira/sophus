package routers

import (
	"sophus/pkg/http/controllers"
	"sophus/pkg/http/middlewares"
	"sophus/utils/env"

	"github.com/kataras/iris/v12"
)

func Router(r *iris.Application) {
	r.Get("/login", controllers.Login)
	r.Post("/dologin", controllers.DoLogin)

	r.Post("/webhook/{webhookId:uuid}", middlewares.AuthWebhook, controllers.Webhook)

	api := r.Party("/api")
	api.Use(middlewares.AuthAPI)
	{
		message := api.Party("/message")
		{
			message.Post("/send", controllers.SendMessage)
		}
		api.Get("/departments", controllers.GetDepartmentSettings)
		api.Post("/departments", controllers.CreateDepartment)
		api.Delete("/departments/{id:int}", controllers.DeleteDepartment)
		api.Put("/departments/agents/{id:int}", controllers.UpdateAgentDepartments)
	}

	web := r.Party("/")
	web.Use(middlewares.AuthLogin)

	medias := web.Party("/medias")
	medias.Use(middlewares.AuthMediaCompany)
	medias.HandleDir("/", iris.Dir(env.Backend["MEDIA_DIRECTORY"]), iris.DirOptions{
		Attachments: iris.Attachments{Enable: true},
	})

	web.Get("/sse", middlewares.SSEMessages)
	web.Get("/sse/conversations", middlewares.SSEConversations)

	web.Get("/messages", controllers.Messages)
	web.Get("/messages/list", controllers.ConversationList)
	web.Get("/messages/{url:uuid}", controllers.MessageOpen)
	web.Post("/messages/{url:uuid}/close", controllers.CloseConversation)
	web.Post("/messages/{url:uuid}/accept", controllers.AcceptConversation)
	web.Post("/messages/{url:uuid}/ignore", controllers.IgnoreConversation)

	web.Get("/departments", controllers.DepartmentSettingsPage)

	flows := web.Party("/flows")
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
			flowsAPI.Get("/assignment-options", controllers.ListAssignmentOptions)
			flowsAPI.Get("/{id:int}", controllers.GetFlow)
			flowsAPI.Post("/", controllers.CreateFlow)
			flowsAPI.Put("/{id:int}", controllers.UpdateFlow)
			flowsAPI.Delete("/{id:int}", controllers.DeleteFlow)
			flowsAPI.Post("/{id:int}/execute", controllers.ExecuteFlowTest)
		}
	}

	instance := web.Party("/instances")
	{
		instance.Get("/", controllers.Instances)
		instance.Get("/create", controllers.InstancePopup)
		instance.Post("/create", controllers.NewInstance)
		instance.Get("/{id:int}/qrcode/events", middlewares.SSEQRCode)
	}

}
