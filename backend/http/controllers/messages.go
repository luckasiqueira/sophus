package controllers

import (
	"zubly/backend/database"

	"github.com/kataras/iris/v12"
)

func MessageReceived(ctx iris.Context) {
	database.MessageReceived()
}

func MessageSent(ctx iris.Context) {

}
