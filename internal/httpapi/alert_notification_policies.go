package httpapi

import (
	"net/http"
	"strings"

	"novaobs/internal/alerting"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

func listNotificationPoliciesHandler(service alerting.PolicyService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		items, err := service.List(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Query("service_id")), ctx.Query("enabled") == "true")
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func createNotificationPolicyHandler(service alerting.PolicyService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.CreateNotificationPolicyRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "通知策略请求格式不正确")
			return
		}
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		item, err := service.Create(ctx.Request.Context(), subject, body)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.Created(ctx, item)
	}
}

func updateNotificationPolicyHandler(service alerting.PolicyService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.UpdateNotificationPolicyRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "通知策略请求格式不正确")
			return
		}
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		item, err := service.Update(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, item, gin.H{})
	}
}
