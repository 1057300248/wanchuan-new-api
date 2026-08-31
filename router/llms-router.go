package router

import (
	"github.com/QuantumNous/new-api/controller"

	"github.com/gin-gonic/gin"
)

func SetLlmsRouter(router *gin.Engine) {
	router.GET("/llms.txt", controller.GetLlmsIndex)
	router.HEAD("/llms.txt", controller.GetLlmsIndex)
	router.GET("/llms-full.txt", controller.GetLlmsFull)
	router.HEAD("/llms-full.txt", controller.GetLlmsFull)
}
