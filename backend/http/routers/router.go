package routers

import "github.com/kataras/iris/v12"

func Router(r *iris.Application) {
	r.Any("/", func(ctx iris.Context) {
		ctx.HTML("<h1>Hello World</h1>")
	})
}
