package auth

import (
	"errors"
	"net/http"
	"time"

	"novaobs/internal/platform/authctx"
	"novaobs/pkg/response"

	"github.com/gin-gonic/gin"
)

const SessionCookieName = "novaobs_session"

func LoginHandler(service *Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if service == nil {
			response.Error(ctx, http.StatusServiceUnavailable, "auth_unavailable", "平台认证服务不可用")
			return
		}
		var req LoginRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.Error(ctx, http.StatusBadRequest, "invalid_request", "登录参数无效")
			return
		}
		session, token, err := service.Login(ctx.Request.Context(), req)
		if err != nil {
			writeError(ctx, err)
			return
		}
		setSessionCookie(ctx, token, session.ExpiresAt)
		response.OK(ctx, session, gin.H{})
	}
}

func SessionHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		response.OK(ctx, gin.H{"subject": subject}, gin.H{})
	}
}

func LogoutHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		clearSessionCookie(ctx)
		response.OK(ctx, gin.H{"status": "logged_out"}, gin.H{})
	}
}

func SessionMiddleware(service *Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if service == nil {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			ctx.Abort()
			return
		}
		cookie, err := ctx.Request.Cookie(SessionCookieName)
		if err != nil || cookie.Value == "" {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			ctx.Abort()
			return
		}
		session, err := service.Parse(cookie.Value)
		if err != nil {
			clearSessionCookie(ctx)
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "登录会话已失效")
			ctx.Abort()
			return
		}
		ctx.Request = ctx.Request.WithContext(authctx.WithSubject(ctx.Request.Context(), session.Subject))
		ctx.Next()
	}
}

func setSessionCookie(ctx *gin.Context, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(ctx.Writer, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   ctx.Request.TLS != nil,
	})
}

func clearSessionCookie(ctx *gin.Context) {
	http.SetCookie(ctx.Writer, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   ctx.Request.TLS != nil,
	})
}

func writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		response.Error(ctx, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
	case errors.Is(err, ErrInvalidSession):
		response.Error(ctx, http.StatusUnauthorized, "invalid_session", "登录会话已失效")
	default:
		response.Error(ctx, http.StatusInternalServerError, "auth_failed", "平台认证失败")
	}
}
