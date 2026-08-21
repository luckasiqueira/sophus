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
	r.Get("/setup", controllers.SaaSSetupPage)
	r.Post("/setup", controllers.CreateSaaSSetup)
	r.Post("/logout", controllers.Logout)

	r.Post("/webhook/{webhookId:uuid}", middlewares.AuthWebhook, controllers.Webhook)
	r.Post("/payments/mercado-pago/webhook", controllers.MercadoPagoWebhook)
	r.Post("/payments/abacatepay/webhook", controllers.AbacatePayWebhook)
	r.Get("/medias/{companyId:int}/flows/{file:string}", middlewares.AuthFlowMedia, controllers.ServeFlowMedia)
	r.Get("/", middlewares.AuthUser, controllers.Home)
	r.Get("/settings", middlewares.AuthUser, controllers.SettingsPage)
	r.Get("/companies/{id:int}", middlewares.AuthUser, controllers.CompanyOverviewPage)

	managementAPI := r.Party("/management/api")
	managementAPI.Use(middlewares.AuthUser)
	{
		managementAPI.Get("/overview", controllers.SaaSOverview)
		managementAPI.Get("/settings", controllers.GetSettings)
		managementAPI.Put("/settings", controllers.UpdateSettings)
		managementAPI.Put("/profile", controllers.UpdateSaaSProfile)
		managementAPI.Post("/plans", controllers.CreateSaaSPlan)
		managementAPI.Put("/plans/{id:int}", controllers.UpdateSaaSPlan)
		managementAPI.Post("/companies", controllers.CreateSaaSCompany)
		managementAPI.Put("/companies/{id:int}", controllers.UpdateSaaSCompany)
		managementAPI.Put("/companies/{id:int}/status", controllers.SetSaaSCompanyStatus)
		managementAPI.Put("/companies/{id:int}/plan", controllers.ChangeSaaSCompanyPlan)
		managementAPI.Put("/companies/{id:int}/due-date", controllers.UpdateSaaSCompanyDueDate)
		managementAPI.Get("/companies/{id:int}/payments", controllers.ListSaaSCompanyPayments)
		managementAPI.Get("/companies/{id:int}/overview", controllers.GetCompanyOperationalOverview)
		managementAPI.Post("/companies/{id:int}/checkout", controllers.CreateSaaSCompanyCheckout)
		managementAPI.Get("/agents", controllers.ListCompanyAgents)
		managementAPI.Post("/agents", controllers.CreateCompanyAgent)
		managementAPI.Put("/agents/{id:int}", controllers.UpdateCompanyAgent)
		managementAPI.Get("/billing", controllers.GetCompanyBilling)
		managementAPI.Post("/billing/checkout", controllers.CreateCompanyCheckout)
		managementAPI.Put("/billing/cycle", controllers.UpdateCompanyBillingCycle)
	}

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
	medias.HandleDir("/", iris.Dir(env.Backend["MEDIA_DIRECTORY"]), iris.DirOptions{})

	web.Get("/sse", middlewares.AuthSubscription, middlewares.SSEMessages)
	web.Get("/sse/conversations", middlewares.AuthSubscription, middlewares.SSEConversations)

	web.Get("/messages", middlewares.AuthSubscription, controllers.Messages)
	web.Get("/messages/list", middlewares.AuthSubscription, controllers.ConversationList)
	web.Get("/messages/{url:uuid}", middlewares.AuthSubscription, controllers.MessageOpen)
	web.Post("/messages/{url:uuid}/close", middlewares.AuthSubscription, controllers.CloseConversation)
	web.Post("/messages/{url:uuid}/accept", middlewares.AuthSubscription, controllers.AcceptConversation)
	web.Post("/messages/{url:uuid}/ignore", middlewares.AuthSubscription, controllers.IgnoreConversation)

	web.Get("/departments", controllers.DepartmentSettingsPage)
	web.Get("/agents", controllers.AgentSettingsPage)
	web.Get("/billing", controllers.BillingPage)

	flows := web.Party("/flows")
	flows.Use(middlewares.AuthSubscription)
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
			flowsAPI.Post("/parse-curl", controllers.ParseFlowCURL)
			flowsAPI.Post("/media", controllers.UploadFlowMedia)
			flowsAPI.Get("/templates", controllers.ListFlowTemplates)
			flowsAPI.Post("/templates", controllers.CreateFlowTemplate)
			flowsAPI.Put("/templates/{id:int}", controllers.UpdateFlowTemplate)
			flowsAPI.Delete("/templates/{id:int}", controllers.DeleteFlowTemplate)
			flowsAPI.Get("/{id:int}", controllers.GetFlow)
			flowsAPI.Post("/", controllers.CreateFlow)
			flowsAPI.Put("/{id:int}", controllers.UpdateFlow)
			flowsAPI.Delete("/{id:int}", controllers.DeleteFlow)
			flowsAPI.Post("/{id:int}/execute", controllers.ExecuteFlowTest)
		}
	}

	instance := web.Party("/instances")
	instance.Use(middlewares.AuthSubscription)
	{
		instance.Get("/", controllers.Instances)
		instance.Get("/list", controllers.InstanceList)
		instance.Get("/events", middlewares.SSEInstances)
		instance.Get("/create", controllers.InstancePopup)
		instance.Post("/create", controllers.NewInstance)
		instance.Post("/{id:int}/reconnect", controllers.ReconnectInstance)
		instance.Get("/{id:int}/qrcode/events", middlewares.SSEQRCode)
	}

}
