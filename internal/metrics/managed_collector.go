package metrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	k8sdeployment "novaapm/internal/modules/k8sops/deployment"
	k8sresource "novaapm/internal/modules/k8sops/resource"
	platformimages "novaapm/internal/platform/images"
	platformrbac "novaapm/internal/platform/rbac"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	ErrManagedCollectorUnsupported = errors.New("managed_collector_unsupported")
	ErrCollectorReleaseMismatch    = errors.New("collector_release_mismatch")
	ErrCollectorAlreadyPresent     = errors.New("metrics_collector_already_present")
)

type ManagedCollectorDeployer interface {
	Preview(ctx context.Context, subject platformrbac.Subject, request k8sdeployment.OperationRequest) (k8sdeployment.OperationResult, error)
	Apply(ctx context.Context, subject platformrbac.Subject, request k8sdeployment.OperationRequest) (k8sdeployment.OperationResult, error)
}

type ImageTemplateReader interface {
	TemplateValues(ctx context.Context) (map[string]string, error)
}

func (s IntegrationService) PreviewManagedCollector(ctx context.Context, subject platformrbac.Subject, sourceID string, request PreviewCollectorReleaseRequest) (CollectorRelease, error) {
	source, integration, clusterID, destinationURL, err := s.managedCollectorContext(ctx, subject, sourceID)
	if err != nil {
		return CollectorRelease{}, err
	}
	if s.k8sResources == nil {
		return CollectorRelease{}, ErrManagedCollectorUnsupported
	}
	resources, err := s.k8sResources.List(ctx, k8sresource.ListFilter{ClusterID: clusterID, Page: 1, PageSize: 2000})
	if err != nil {
		return CollectorRelease{}, err
	}
	collector, _ := detectKubernetesMetricsStack(resources)
	if collector != "" {
		return CollectorRelease{}, ErrCollectorAlreadyPresent
	}
	namespace := strings.TrimSpace(request.Namespace)
	if namespace == "" {
		namespace = "novaapm-system"
	}
	if !validKubernetesName(namespace) {
		return CollectorRelease{}, fmt.Errorf("namespace 无效")
	}
	image, err := s.vmagentImage(ctx)
	if err != nil {
		return CollectorRelease{}, err
	}
	manifest := renderManagedVMAgent(namespace, image, destinationURL, integration.EnvironmentID)
	preview, err := s.deployer.Preview(ctx, subject, k8sdeployment.OperationRequest{ClusterID: clusterID, YAMLContent: manifest})
	if err != nil {
		return CollectorRelease{}, err
	}
	generation := int64(1)
	if latest, err := s.repository.GetLatestCollectorRelease(ctx, source.ID); err == nil {
		generation = latest.Generation + 1
	}
	now := s.now()
	release := CollectorRelease{ID: primitive.NewObjectID().Hex(), SourceAccessID: source.ID, Generation: generation, ClusterID: clusterID, Namespace: namespace, Image: image, ManifestHash: hashManifest(manifest), Status: ReleasePreviewed, PreviewID: preview.PreviewID, ConfirmationToken: preview.ConfirmationToken, Resources: managedResources(preview.Resources), Message: preview.Message, CreatedBy: subject.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.SaveCollectorRelease(ctx, release); err != nil {
		return CollectorRelease{}, err
	}
	return release, nil
}

func (s IntegrationService) ApplyManagedCollector(ctx context.Context, subject platformrbac.Subject, sourceID string) (CollectorRelease, error) {
	source, integration, clusterID, destinationURL, err := s.managedCollectorContext(ctx, subject, sourceID)
	if err != nil {
		return CollectorRelease{}, err
	}
	release, err := s.repository.GetLatestCollectorRelease(ctx, source.ID)
	if err != nil || release.Status != ReleasePreviewed {
		return CollectorRelease{}, ErrCollectorReleaseMismatch
	}
	manifest := renderManagedVMAgent(release.Namespace, release.Image, destinationURL, integration.EnvironmentID)
	if hashManifest(manifest) != release.ManifestHash || release.ClusterID != clusterID {
		return CollectorRelease{}, ErrCollectorReleaseMismatch
	}
	result, err := s.deployer.Apply(ctx, subject, k8sdeployment.OperationRequest{ClusterID: clusterID, YAMLContent: manifest, PreviewID: release.PreviewID, ConfirmationToken: release.ConfirmationToken})
	release.UpdatedAt = s.now()
	if err != nil {
		release.Status = ReleaseFailed
		release.Message = err.Error()
		_ = s.repository.UpdateCollectorRelease(ctx, release)
		return CollectorRelease{}, err
	}
	release.Status, release.Message, release.Resources = ReleaseApplied, result.Message, managedResources(result.Resources)
	if err := s.repository.UpdateCollectorRelease(ctx, release); err != nil {
		return CollectorRelease{}, err
	}
	return release, nil
}

func (s IntegrationService) managedCollectorContext(ctx context.Context, subject platformrbac.Subject, sourceID string) (SourceAccess, Integration, string, string, error) {
	if s.deployer == nil || s.imageTemplates == nil {
		return SourceAccess{}, Integration{}, "", "", ErrManagedCollectorUnsupported
	}
	source, err := s.repository.GetSourceAccess(ctx, strings.TrimSpace(sourceID))
	if err != nil {
		return SourceAccess{}, Integration{}, "", "", err
	}
	integration, err := s.repository.GetIntegration(ctx, source.IntegrationID)
	if err != nil {
		return SourceAccess{}, Integration{}, "", "", err
	}
	if !s.allowed(subject, integration.EnvironmentID, "metrics.deployment", "manage") {
		return SourceAccess{}, Integration{}, "", "", ErrPermissionDenied
	}
	if source.SourceKind != SourceKindKubernetesInfra || source.CollectionMode != CollectionModeManaged {
		return SourceAccess{}, Integration{}, "", "", ErrManagedCollectorUnsupported
	}
	bindings, err := s.environments.ListResourceBindings(ctx, integration.EnvironmentID)
	if err != nil {
		return SourceAccess{}, Integration{}, "", "", err
	}
	clusterID := ""
	for _, binding := range bindings {
		if binding.ID == source.ResourceBindingID {
			clusterID = binding.ResourceRef
			break
		}
	}
	reader, ok := s.destinations.(MetricsDestinationOptionsReader)
	if !ok {
		return SourceAccess{}, Integration{}, "", "", ErrDestinationUnavailable
	}
	destination, err := reader.GetOption(ctx, integration.DestinationRef)
	if err != nil || strings.TrimSpace(destination.URLs.RemoteWriteURL) == "" {
		return SourceAccess{}, Integration{}, "", "", ErrDestinationUnavailable
	}
	if clusterID == "" {
		return SourceAccess{}, Integration{}, "", "", ErrEnvironmentUnavailable
	}
	return source, integration, clusterID, destination.URLs.RemoteWriteURL, nil
}

func (s IntegrationService) vmagentImage(ctx context.Context) (string, error) {
	values, err := s.imageTemplates.TemplateValues(ctx)
	if err != nil {
		return "", err
	}
	image := strings.TrimSpace(values[platformimages.VMAgentImagePlaceholder])
	if image == "" {
		return "", fmt.Errorf("vmagent 镜像未配置")
	}
	return image, nil
}

func managedResources(items []k8sdeployment.ResourceIdentity) []ManagedResource {
	out := make([]ManagedResource, 0, len(items))
	for _, item := range items {
		out = append(out, ManagedResource{APIVersion: item.APIVersion, Kind: item.Kind, Namespace: item.Namespace, Name: item.Name})
	}
	return out
}

func hashManifest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validKubernetesName(value string) bool {
	if value == "" || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func renderManagedVMAgent(namespace string, image string, remoteWriteURL string, environmentID string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: novaapm-vmagent
  namespace: %s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: novaapm-vmagent
rules:
  - apiGroups: [""]
    resources: ["nodes", "nodes/proxy", "services", "endpoints", "pods"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: novaapm-vmagent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: novaapm-vmagent
subjects:
  - kind: ServiceAccount
    name: novaapm-vmagent
    namespace: %s
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: novaapm-vmagent
  namespace: %s
data:
  scrape.yml: |
    global:
      scrape_interval: 30s
    scrape_configs:
      - job_name: kubernetes-kubelet
        scheme: https
        bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
        tls_config:
          ca_file: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
        kubernetes_sd_configs:
          - role: node
        relabel_configs:
          - target_label: __address__
            replacement: kubernetes.default.svc:443
          - source_labels: [__meta_kubernetes_node_name]
            target_label: __metrics_path__
            replacement: /api/v1/nodes/$1/proxy/metrics
      - job_name: kubernetes-cadvisor
        scheme: https
        bearer_token_file: /var/run/secrets/kubernetes.io/serviceaccount/token
        tls_config:
          ca_file: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
        kubernetes_sd_configs:
          - role: node
        relabel_configs:
          - target_label: __address__
            replacement: kubernetes.default.svc:443
          - source_labels: [__meta_kubernetes_node_name]
            target_label: __metrics_path__
            replacement: /api/v1/nodes/$1/proxy/metrics/cadvisor
      - job_name: kubernetes-infra-services
        kubernetes_sd_configs:
          - role: endpoints
        relabel_configs:
          - source_labels: [__meta_kubernetes_service_name]
            action: keep
            regex: (node-exporter|kube-state-metrics|kube-dns|coredns)
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: novaapm-vmagent
  namespace: %s
  labels:
    app.kubernetes.io/name: novaapm-vmagent
    app.kubernetes.io/managed-by: novaapm
spec:
  replicas: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: novaapm-vmagent
  template:
    metadata:
      labels:
        app.kubernetes.io/name: novaapm-vmagent
    spec:
      serviceAccountName: novaapm-vmagent
      containers:
        - name: vmagent
          image: %s
          args:
            - -promscrape.config=/etc/vmagent/scrape.yml
            - %s
            - %s
            - -remoteWrite.tmpDataPath=/var/lib/vmagent
          ports:
            - name: http
              containerPort: 8429
          readinessProbe:
            httpGet: {path: /health, port: http}
          resources:
            requests: {cpu: 100m, memory: 128Mi}
            limits: {cpu: "1", memory: 1Gi}
          volumeMounts:
            - {name: config, mountPath: /etc/vmagent, readOnly: true}
            - {name: queue, mountPath: /var/lib/vmagent}
      volumes:
        - name: config
          configMap: {name: novaapm-vmagent}
        - name: queue
          emptyDir: {}
`, namespace, namespace, namespace, namespace, namespace, strconv.Quote(image), strconv.Quote("-remoteWrite.url="+remoteWriteURL), strconv.Quote("-remoteWrite.label="+EnvironmentIdentityLabel+"="+environmentID))
}
