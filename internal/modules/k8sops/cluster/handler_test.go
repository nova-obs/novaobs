package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"novaobs/internal/modules/k8sops/kubeclient"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClusterHandlersCreateAndListPersistedClusters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := NewMemoryRepository(nil)
	service := NewService(&repo)
	router := gin.New()
	api := router.Group("/api/v1")
	api.POST("/k8s/clusters", CreateHandler(service))
	api.GET("/k8s/clusters", ListHandler(service))
	api.DELETE("/k8s/clusters/:id", DeleteHandler(service))

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/clusters", strings.NewReader(`{"id":"prod","name":"prod-core","version":"v1.30.1","region":"cn-shanghai","description":"生产集群"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(createRecorder, createRequest)

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/clusters?q=core", nil)
	router.ServeHTTP(listRecorder, listRequest)

	require.Equal(t, http.StatusCreated, createRecorder.Code)
	require.Equal(t, http.StatusOK, listRecorder.Code)
	require.Contains(t, listRecorder.Body.String(), `"id":"prod"`)
	require.Contains(t, listRecorder.Body.String(), `"status":"active"`)

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/k8s/clusters/prod", nil)
	router.ServeHTTP(deleteRecorder, deleteRequest)

	listAfterDeleteRecorder := httptest.NewRecorder()
	router.ServeHTTP(listAfterDeleteRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/k8s/clusters?q=core", nil))

	require.Equal(t, http.StatusOK, deleteRecorder.Code)
	require.Contains(t, deleteRecorder.Body.String(), `"deleted":true`)
	require.NotContains(t, listAfterDeleteRecorder.Body.String(), `"id":"prod"`)
}

func TestClusterCreateRejectsMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := NewMemoryRepository(nil)
	service := NewService(&repo)
	router := gin.New()
	router.POST("/api/v1/k8s/clusters", CreateHandler(service))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/clusters", strings.NewReader(`{"name":""}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request")
}

func TestClusterCapabilityHandlerReturnsSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := NewCapabilityService(staticHandlerCapabilityProvider{snapshot: kubeclient.CapabilitySnapshot{
		ServerVersion: "v1.30.2",
		Resources: []kubeclient.APIResource{
			{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true},
		},
	}})
	router := gin.New()
	router.GET("/api/v1/k8s/clusters/:id/capabilities", CapabilityHandler(service))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/clusters/prod/capabilities", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"cluster_id":"prod"`)
	require.Contains(t, recorder.Body.String(), `"server_version":"v1.30.2"`)
	require.Contains(t, recorder.Body.String(), `"horizontalpodautoscalers"`)
}

func TestClusterProbeHandlerReturnsPolicyAndCapabilitySummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := NewMemoryRepository([]Cluster{{ID: "test03", Name: "test03", Status: "active", ReadOnly: true}})
	clusterService := NewService(repo)
	capabilityService := NewCapabilityService(staticHandlerCapabilityProvider{snapshot: kubeclient.CapabilitySnapshot{
		ServerVersion: "v1.30.2",
		Resources: []kubeclient.APIResource{
			{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true},
		},
	}})
	router := gin.New()
	router.POST("/api/v1/k8s/clusters/:id/probe", ProbeHandler(clusterService, capabilityService))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/clusters/test03/probe", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"cluster_id":"test03"`)
	require.Contains(t, recorder.Body.String(), `"read_only":true`)
	require.Contains(t, recorder.Body.String(), `"access_mode":"direct"`)
	require.Contains(t, recorder.Body.String(), `"server_version":"v1.30.2"`)
	require.Contains(t, recorder.Body.String(), `"resource_count":1`)
}

func TestClusterProbeHandlerReportsMissingCluster(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/k8s/clusters/:id/probe", ProbeHandler(NewService(NewMemoryRepository(nil)), NewCapabilityService(staticHandlerCapabilityProvider{})))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/k8s/clusters/missing/probe", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Body.String(), "not_found")
}

func TestClusterCapabilityHandlerReportsUnavailableProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/k8s/clusters/:id/capabilities", CapabilityHandler(NewCapabilityService(nil)))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/clusters/prod/capabilities", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "k8s_cluster_capability_unavailable")
}

type staticHandlerCapabilityProvider struct {
	snapshot kubeclient.CapabilitySnapshot
}

func (p staticHandlerCapabilityProvider) Capabilities(_ context.Context, clusterID string) (kubeclient.CapabilitySnapshot, error) {
	p.snapshot.ClusterID = clusterID
	return p.snapshot, nil
}

func TestClusterDeleteRejectsMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := NewMemoryRepository(nil)
	service := NewService(&repo)
	router := gin.New()
	router.DELETE("/api/v1/k8s/clusters/:id", DeleteHandler(service))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/k8s/clusters/%20", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "invalid_request")
}
