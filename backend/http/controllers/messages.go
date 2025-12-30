package controllers

import (
	"zubly/backend/database"

	"github.com/kataras/iris/v12"
)

func MessageIncoming(ctx iris.Context) {
	database.MessageIncoming()
}

func MessageOutgoing(ctx iris.Context) {

}
