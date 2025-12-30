package routers

import (
	"zubly/backend/http/controllers"

	"github.com/kataras/iris/v12"
)

func Router(r *iris.Application) {
	r.Any("/", func(ctx iris.Context) {
		ctx.HTML("<h1>Hello World</h1>")
	})

	message := r.Party("/message")
	{
		message.Post("/in", controllers.MessageIncoming)
	}

}
