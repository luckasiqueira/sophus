package http

import (
	"zubly/backend/pkg/http/routers"

	"github.com/kataras/iris/v12"
)

func Start(port string) {
	srv := iris.Default()
	routers.Router(srv)
	srv.Listen(":" + port)
}
