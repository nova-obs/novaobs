package cluster

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
