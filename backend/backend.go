package main

import (
	"sophus/backend/pkg/http"
	"sophus/backend/utils/env"
)

func main() {
	http.Start(env.Backend["HTTP_PORT"])
}
