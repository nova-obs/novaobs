package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"novaapm/internal/alerting"
	"novaapm/internal/logs"
	k8sopscluster "novaapm/internal/modules/k8sops/cluster"
	k8sopsdeployment "novaapm/internal/modules/k8sops/deployment"
	"novaapm/internal/platform/authctx"
	"novaapm/pkg/apperr"
	"novaapm/pkg/response"

	"github.com/gin-gonic/gin"
)

func getLogsOnboardingWorkspaceHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		workspace, err := service.Workspace(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("productId")), strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeError(ctx, err)
			return
		}
		response.OK(ctx, workspace, gin.H{})
	}
}

func getLogsK8sWorkloadsHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		items, err := service.ListK8sWorkloads(ctx.Request.Context(), ctx.Query("cluster_id"), ctx.Query("namespace"))
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func syncLogsK8sServicesHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.SyncK8sNamespaceRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("K8s 服务同步请求无效"))
			return
		}
		productID := strings.TrimSpace(ctx.Param("productId"))
		if body.ProductID != "" && strings.TrimSpace(body.ProductID) != productID {
			writeError(ctx, apperr.InvalidRequest("请求产品与路径产品不一致"))
			return
		}
		body.ProductID = productID
		result, err := service.SyncK8sNamespaceServices(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{"total": result.Total})
	}
}

func createLogsEndpointHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		var body logEndpointRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志下游端点请求无效"))
			return
		}
		endpoint, err := service.CreateEndpointForSubject(ctx.Request.Context(), subject, body.endpoint())
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.Created(ctx, endpoint)
	}
}

func listLogsEndpointsHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		endpoints, err := service.ListEndpointsForSubject(ctx.Request.Context(), subject)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, endpoints, gin.H{"total": len(endpoints)})
	}
}

func updateLogsEndpointHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		var body logEndpointRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志下游端点请求无效"))
			return
		}
		endpoint, err := service.UpdateEndpointForSubject(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body.endpoint())
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, endpoint, gin.H{})
	}
}

type logEndpointRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	SignalTypes []string `json:"signal_types"`
	SinkType    string   `json:"sink_type"`
	StreamName  string   `json:"stream_name"`
	WriteURL    string   `json:"write_url"`
	QueryURL    string   `json:"query_url"`
	VMUIURL     string   `json:"vmui_url"`
	ScopeType   string   `json:"scope_type"`
	ClusterID   string   `json:"cluster_id"`
	Status      string   `json:"status"`
}

func (req logEndpointRequest) endpoint() logs.LogEndpoint {
	return logs.LogEndpoint{
		Name:        req.Name,
		Description: req.Description,
		Kind:        req.Kind,
		SignalTypes: req.SignalTypes,
		SinkType:    req.SinkType,
		StreamName:  req.StreamName,
		WriteURL:    req.WriteURL,
		QueryURL:    req.QueryURL,
		VMUIURL:     req.VMUIURL,
		ScopeType:   req.ScopeType,
		ClusterID:   req.ClusterID,
		Status:      req.Status,
	}
}

func listLogsTargetsHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		targets, err := service.ListTargets(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Query("service_id")))
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, targets, gin.H{"total": len(targets)})
	}
}

func createLogsTargetHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		var body logs.CreateLogTargetRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志目标登记请求无效"))
			return
		}
		target, err := service.CreateTarget(ctx.Request.Context(), subject, body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.Created(ctx, target)
	}
}

func updateLogsTargetHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		var body logs.UpdateLogTargetRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志目标更新请求无效"))
			return
		}
		target, err := service.UpdateTarget(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, target, gin.H{})
	}
}

func probeLogsTargetHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		target, err := service.ProbeTarget(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, target, gin.H{})
	}
}

func publishLogsEndpointVmalertRuntimeHandler(service alerting.LogRuntimeService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body alerting.LogRuntimePublishRequest
		if err := ctx.ShouldBindJSON(&body); err != nil && ctx.Request.ContentLength > 0 {
			writeError(ctx, apperr.InvalidRequest("vmalert Runtime 发布请求无效"))
			return
		}
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		result, err := service.Publish(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func previewLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.UpsertRouteRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志接入预览请求无效"))
			return
		}
		if !authorizeLogsServiceRoute(ctx, service, body.ServiceID, "manage") {
			return
		}
		preview, err := service.PreviewRoute(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, preview, gin.H{})
	}
}

func previewLogsParseRulesHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.ParsePreviewRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志解析预览请求无效"))
			return
		}
		result, err := service.PreviewParseRules(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func createLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.UpsertRouteRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志接入路由请求无效"))
			return
		}
		if !authorizeLogsServiceRoute(ctx, service, body.ServiceID, "manage") {
			return
		}
		route, err := service.CreateRoute(ctx.Request.Context(), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.Created(ctx, route)
	}
}

func updateLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.UpsertRouteRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeError(ctx, apperr.InvalidRequest("日志接入路由请求无效"))
			return
		}
		if !authorizeLogsRoute(ctx, service, strings.TrimSpace(ctx.Param("id")), "manage") {
			return
		}
		route, err := service.UpdateRoute(ctx.Request.Context(), strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, route, gin.H{})
	}
}

func getLogsRouteCollectorConfigHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !authorizeLogsRoute(ctx, service, strings.TrimSpace(ctx.Param("id")), "read") {
			return
		}
		config, err := service.RouteCollectorConfig(ctx.Request.Context(), strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, config, gin.H{})
	}
}

func getVMInstallationHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		routeID := strings.TrimSpace(ctx.Param("id"))
		if !authorizeLogsRoute(ctx, service, routeID, "manage") {
			return
		}
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		artifact, err := service.VMInstallation(ctx.Request.Context(), subject, routeID)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, artifact, gin.H{})
	}
}

func listVMLogAgentEndpointsHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		routeID := strings.TrimSpace(ctx.Param("id"))
		if !authorizeLogsRoute(ctx, service, routeID, "read") {
			return
		}
		items, err := service.ListVMEndpoints(ctx.Request.Context(), routeID)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, items, gin.H{"total": len(items)})
	}
}

func createVMLogAgentEndpointHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		routeID := strings.TrimSpace(ctx.Param("id"))
		if !authorizeLogsRoute(ctx, service, routeID, "manage") {
			return
		}
		var body logs.UpsertVMEndpointRequest
		if err := ctx.ShouldBindJSON(&body); err != nil {
			writeLogsError(ctx, apperr.InvalidRequest("VM 节点请求无效"))
			return
		}
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		item, err := service.CreateVMEndpoint(ctx.Request.Context(), subject, routeID, body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.Created(ctx, item)
	}
}

func deleteVMLogAgentEndpointHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		routeID := strings.TrimSpace(ctx.Param("id"))
		if !authorizeLogsRoute(ctx, service, routeID, "manage") {
			return
		}
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		if err := service.DeleteVMEndpoint(ctx.Request.Context(), subject, routeID, strings.TrimSpace(ctx.Param("endpointId"))); err != nil {
			writeLogsError(ctx, err)
			return
		}
		ctx.Status(http.StatusNoContent)
	}
}

func probeVMLogAgentEndpointHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		routeID := strings.TrimSpace(ctx.Param("id"))
		if !authorizeLogsRoute(ctx, service, routeID, "manage") {
			return
		}
		subject, _ := authctx.SubjectFrom(ctx.Request.Context())
		item, err := service.ProbeVMEndpoint(ctx.Request.Context(), subject, routeID, strings.TrimSpace(ctx.Param("endpointId")))
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, item, gin.H{})
	}
}

func probeLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !authorizeLogsRoute(ctx, service, strings.TrimSpace(ctx.Param("id")), "manage") {
			return
		}
		result, err := service.ProbeRoute(ctx.Request.Context(), strings.TrimSpace(ctx.Param("id")))
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func deleteLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !authorizeLogsRoute(ctx, service, strings.TrimSpace(ctx.Param("id")), "manage") {
			return
		}
		if err := service.DeleteRoute(ctx.Request.Context(), strings.TrimSpace(ctx.Param("id"))); err != nil {
			writeLogsError(ctx, err)
			return
		}
		ctx.Status(http.StatusNoContent)
	}
}

func publishLogsRouteHandler(service logs.Service) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var body logs.PublishRouteRequest
		if err := ctx.ShouldBindJSON(&body); err != nil && ctx.Request.ContentLength > 0 {
			writeError(ctx, apperr.InvalidRequest("日志接入发布请求无效"))
			return
		}
		subject, ok := authctx.SubjectFrom(ctx.Request.Context())
		if !ok {
			response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		if err := service.AuthorizeRoute(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), "manage"); err != nil {
			writeLogsError(ctx, err)
			return
		}
		result, err := service.PublishRoute(ctx.Request.Context(), subject, strings.TrimSpace(ctx.Param("id")), body)
		if err != nil {
			writeLogsError(ctx, err)
			return
		}
		response.OK(ctx, result, gin.H{})
	}
}

func authorizeLogsServiceRoute(ctx *gin.Context, service logs.Service, serviceID string, action string) bool {
	subject, ok := authctx.SubjectFrom(ctx.Request.Context())
	if !ok {
		response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
		return false
	}
	if err := service.AuthorizeServiceRoute(subject, serviceID, action); err != nil {
		writeLogsError(ctx, err)
		return false
	}
	return true
}

func authorizeLogsRoute(ctx *gin.Context, service logs.Service, routeID string, action string) bool {
	subject, ok := authctx.SubjectFrom(ctx.Request.Context())
	if !ok {
		response.Error(ctx, http.StatusUnauthorized, "unauthorized", "请先登录")
		return false
	}
	if err := service.AuthorizeRoute(ctx.Request.Context(), subject, routeID, action); err != nil {
		writeLogsError(ctx, err)
		return false
	}
	return true
}

func writeLogsError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, logs.ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权访问该服务的日志资源")
	case errors.Is(err, k8sopscluster.ErrClusterReadOnly):
		response.Error(ctx, http.StatusForbidden, "k8s_cluster_read_only", "当前集群为只读接入，只能生成配置预览，不能发布 Agent")
	case errors.Is(err, k8sopsdeployment.ErrPermissionDenied):
		response.Error(ctx, http.StatusForbidden, "permission_denied", "无权发布 K8s 日志采集 Agent")
	case errors.Is(err, k8sopsdeployment.ErrConfirmationMismatch):
		response.Error(ctx, http.StatusBadRequest, "confirmation_mismatch", "预览确认已失效，请重新预览后再执行")
	case errors.Is(err, k8sopsdeployment.ErrInvalidRequest):
		response.Error(ctx, http.StatusBadRequest, "invalid_request", "K8s Agent 发布请求无效")
	default:
		writeError(ctx, err)
	}
}
