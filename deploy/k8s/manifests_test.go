package k8s_test

import (
	"os"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/stretchr/testify/require"
)

func readManifest(t *testing.T, path string, target any) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(content, target))
}

func findContainer(t *testing.T, deployment appsv1.Deployment, name string) corev1.Container {
	t.Helper()
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == name {
			return container
		}
	}
	t.Fatalf("Deployment %s 缺少容器 %s", deployment.Name, name)
	return corev1.Container{}
}

func TestBackendDeploymentContract(t *testing.T) {
	var deployment appsv1.Deployment
	readManifest(t, "20-backend-deployment.yaml", &deployment)
	container := findContainer(t, deployment, "backend")

	require.Equal(t, "novaapm-backend", deployment.Name)
	require.Equal(t, int32(8080), container.Ports[0].ContainerPort)
	require.Equal(t, "/api/v1/health", container.LivenessProbe.HTTPGet.Path)
	require.Equal(t, "/api/v1/health", container.ReadinessProbe.HTTPGet.Path)
	require.True(t, *container.SecurityContext.RunAsNonRoot)
	require.True(t, *container.SecurityContext.ReadOnlyRootFilesystem)
	require.False(t, *container.SecurityContext.AllowPrivilegeEscalation)
	require.Contains(t, container.SecurityContext.Capabilities.Drop, corev1.Capability("ALL"))
	require.False(t, *deployment.Spec.Template.Spec.AutomountServiceAccountToken)
}

func TestServicesExposeOnlyFrontendViaNodePort(t *testing.T) {
	var backend corev1.Service
	readManifest(t, "21-backend-service.yaml", &backend)
	require.Equal(t, corev1.ServiceTypeClusterIP, backend.Spec.Type)
	require.Equal(t, int32(8080), backend.Spec.Ports[0].TargetPort.IntVal)

	var frontend corev1.Service
	readManifest(t, "31-frontend-service.yaml", &frontend)
	require.Equal(t, corev1.ServiceTypeNodePort, frontend.Spec.Type)
	require.Equal(t, int32(8080), frontend.Spec.Ports[0].TargetPort.IntVal)
	require.Equal(t, int32(30080), frontend.Spec.Ports[0].NodePort)
}

func TestConfigMapDoesNotContainSecrets(t *testing.T) {
	var configMap corev1.ConfigMap
	readManifest(t, "10-backend-config.yaml", &configMap)
	config := configMap.Data["config.yaml"]

	require.Contains(t, config, `host: "0.0.0.0"`)
	require.Contains(t, config, `mode: "release"`)
	require.NotContains(t, strings.ToLower(config), "password")
	require.NotContains(t, strings.ToLower(config), "secret")
	require.NotContains(t, config, "mongodb://")
}

func TestBackendDockerfileDoesNotCopyLocalConfig(t *testing.T) {
	content, err := os.ReadFile("../../Dockerfile")
	require.NoError(t, err)
	dockerfile := string(content)

	require.Contains(t, dockerfile, "AS builder")
	require.Contains(t, dockerfile, "CGO_ENABLED=0")
	require.NotContains(t, dockerfile, "COPY configs")
	require.Contains(t, dockerfile, "USER 10001:10001")
}

func TestMakefileDefaultsToLinuxAMD64DockerBuild(t *testing.T) {
	content, err := os.ReadFile("../../Makefile")
	require.NoError(t, err)
	makefile := string(content)

	require.Contains(t, makefile, "PLATFORM ?= linux/amd64")
	require.Contains(t, makefile, "IMAGE_NAME ?= novaapm-backend")
	require.Contains(t, makefile, "docker-build:")
	require.Contains(t, makefile, "buildx build --platform $(PLATFORM) --load")
	require.Contains(t, makefile, "docker-build-push:")
	require.Contains(t, makefile, "buildx build --platform $(PLATFORM) --push")
}
