package main

import (
	"zubly/backend/http"
	"zubly/backend/utils/env"
)

func main() {
	http.Start(env.Backend["HTTP_PORT"])
}
