package httpapi

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"novaapm/internal/alerting"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

const maxAlertIngestBytes = 1 << 20

type webhookRateWindow struct {
	startedAt time.Time
	count     int
}

type webhookRateLimiter struct {
	mu      sync.Mutex
	windows map[string]webhookRateWindow
}

func alertIngestHandler(ingestor alerting.EventIngestor) gin.HandlerFunc {
	limiter := &webhookRateLimiter{windows: map[string]webhookRateWindow{}}
	return func(ctx *gin.Context) {
		if !limiter.Allow(remoteHost(ctx.Request.RemoteAddr), time.Now()) {
			response.Error(ctx, http.StatusTooManyRequests, "rate_limited", "告警接入请求过于频繁")
			return
		}
		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxAlertIngestBytes)
		var alerts []alerting.AlertIngestAlert
		if err := json.NewDecoder(ctx.Request.Body).Decode(&alerts); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "告警接入请求格式不正确")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(ctx.GetHeader("Authorization"), "Bearer "))
		count, err := ingestor.IngestAlerts(ctx.Request.Context(), token, alerts)
		if err != nil {
			if errors.Is(err, alerting.ErrPermissionDenied) {
				response.Error(ctx, http.StatusUnauthorized, "unauthorized", "告警接入凭据无效")
				return
			}
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"accepted": count}, gin.H{})
	}
}

func (l *webhookRateLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	window := l.windows[key]
	if window.startedAt.IsZero() || now.Sub(window.startedAt) >= time.Minute {
		window = webhookRateWindow{startedAt: now}
	}
	window.count++
	l.windows[key] = window
	if len(l.windows) > 1024 {
		for itemKey, item := range l.windows {
			if now.Sub(item.startedAt) >= time.Minute {
				delete(l.windows, itemKey)
			}
		}
	}
	return window.count <= 600
}

func remoteHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil && host != "" {
		return host
	}
	return address
}

func listAlertInstancesHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		items, err := service.ListInstances(ctx.Request.Context(), subject, alerting.AlertInstanceFilter{
			RuleID: strings.TrimSpace(ctx.Query("rule_id")), ServiceID: strings.TrimSpace(ctx.Query("service_id")),
			State: strings.TrimSpace(ctx.Query("state")), Limit: alertListLimit(ctx.Query("limit")),
		})
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func listAlertEventsHandler(service alerting.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := alertSubject(ctx)
		if !ok {
			return
		}
		items, err := service.ListEvents(ctx.Request.Context(), subject, alerting.AlertEventFilter{
			RuleID: strings.TrimSpace(ctx.Query("rule_id")), Fingerprint: strings.TrimSpace(ctx.Query("fingerprint")),
			Limit: alertListLimit(ctx.Query("limit")),
		})
		if err != nil {
			writeAlertingError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}
