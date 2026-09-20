package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// setupTokenGroupRoute is called only after canonical token authentication.
// It changes this request's routing context, never the cached token or database.
func setupTokenGroupRoute(c *gin.Context, token *model.Token, parts []string) (bool, error) {
	group, present, err := service.ParseTokenGroupRoute(parts)
	if !present {
		return false, nil
	}
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusBadRequest, err.Error())
		return true, err
	}
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	_, _, err = service.GetTokenSelectedGroups(userGroup, token, group)
	if err != nil {
		abortWithOpenAiMessage(c, http.StatusForbidden, "group is not available to this API key")
		return true, err
	}
	common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, group)
	// Explicit selection must not silently fall back into a different group.
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, false)
	return true, nil
}
