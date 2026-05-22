package http

import (
	"sophus/backend/pkg/http/routers"

	"github.com/kataras/iris/v12"
)

func Start(port string) error {
	srv := iris.Default()
	routers.Router(srv)
	return srv.Listen(":" + port)
}
