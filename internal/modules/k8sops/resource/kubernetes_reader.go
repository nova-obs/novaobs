package resource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	k8sopscluster "novaapm/internal/modules/k8sops/cluster"
	"novaapm/internal/modules/k8sops/kubeclient"
	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

var (
	ErrUnsupportedKind      = errors.New("k8s_unsupported_kind")
	ErrReadPermissionDenied = errors.New("permission_denied")
	ErrNamespaceRequired    = errors.New("k8s_namespace_required")
)

type ClientsetProvider interface {
	Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error)
}

type BundleProvider interface {
	Bundle(ctx context.Context, clusterID string) (kubeclient.Bundle, error)
}

type ReadAuthorizer interface {
	Authorize(subject platformrbac.Subject, req platformrbac.Request) platformrbac.Decision
}

type KubernetesReader struct {
	clients    ClientsetProvider
	bundles    BundleProvider
	authorizer ReadAuthorizer
}

func NewKubernetesReader(clients ClientsetProvider, dependencies ...any) KubernetesReader {
	reader := KubernetesReader{clients: clients, authorizer: allowReadAuthorizer{}}
	if value, ok := clients.(BundleProvider); ok && value != nil {
		reader.bundles = value
	}
	for _, dependency := range dependencies {
		switch value := dependency.(type) {
		case ReadAuthorizer:
			if value != nil {
				reader.authorizer = value
			}
		case BundleProvider:
			if value != nil {
				reader.bundles = value
			}
		}
	}
	return reader
}

func (r KubernetesReader) List(ctx context.Context, filter ListFilter) ([]ResourceSummary, error) {
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	if filter.Namespace == "" && !clusterScopedListKind(filter.Kind) {
		return nil, ErrNamespaceRequired
	}
	if !r.allowedForKind(ctx, filter.ClusterID, filter.Namespace, filter.Kind) {
		return nil, ErrReadPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, filter.ClusterID)
	if err != nil {
		return nil, normalizeKubernetesReadError(err)
	}
	items := make([]ResourceSummary, 0)
	kinds := listKinds(filter.Kind)
	bestEffort := strings.TrimSpace(filter.Kind) == ""
	for _, kind := range kinds {
		current, err := r.listKind(ctx, client, filter, kind)
		if err != nil {
			if errors.Is(err, ErrUnsupportedKind) && (bestEffort || isKnownListKind(kind)) {
				continue
			}
			return nil, normalizeKubernetesReadError(err)
		}
		items = append(items, current...)
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	if query != "" {
		items = filterResourcesByQuery(items, query)
	}
	sortResources(items, filter.Sort, filter.Order)
	return paginate(items, filter.Page, filter.PageSize), nil
}

func (r KubernetesReader) GetDetail(ctx context.Context, query DetailQuery) (ResourceDetail, error) {
	if !r.allowedForKind(ctx, query.Identity.ClusterID, query.Identity.Namespace, query.Identity.Kind) {
		return ResourceDetail{}, ErrReadPermissionDenied
	}
	object, identity, err := r.getObject(ctx, query.Identity)
	if err != nil {
		return ResourceDetail{}, normalizeKubernetesReadError(err)
	}
	spec, err := objectSpec(object)
	if err != nil {
		return ResourceDetail{}, err
	}
	return ResourceDetail{
		Identity:  identity,
		Status:    statusForObject(object),
		Labels:    object.GetLabels(),
		Spec:      spec,
		UpdatedAt: object.GetCreationTimestamp().Time,
	}, nil
}

func (r KubernetesReader) GetYAML(ctx context.Context, query DetailQuery) (ResourceYAML, error) {
	if !r.allowedForKind(ctx, query.Identity.ClusterID, query.Identity.Namespace, query.Identity.Kind) {
		return ResourceYAML{}, ErrReadPermissionDenied
	}
	object, identity, err := r.getObject(ctx, query.Identity)
	if err != nil {
		return ResourceYAML{}, normalizeKubernetesReadError(err)
	}
	content, err := objectYAML(object, identity)
	if err != nil {
		return ResourceYAML{}, err
	}
	return ResourceYAML{Identity: identity, YAML: content}, nil
}

func (r KubernetesReader) GetPodLogs(ctx context.Context, query PodLogQuery) (PodLogResult, error) {
	if !r.allowed(ctx, query.ClusterID, query.Namespace) {
		return PodLogResult{}, ErrReadPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, query.ClusterID)
	if err != nil {
		return PodLogResult{}, err
	}
	request := client.CoreV1().Pods(query.Namespace).GetLogs(query.Pod, podLogOptions(query.Container))
	stream, err := request.Stream(ctx)
	if err != nil {
		return PodLogResult{}, normalizeKubernetesReadError(err)
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		return PodLogResult{}, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	return PodLogResult{
		Identity:  Identity{ClusterID: query.ClusterID, Namespace: query.Namespace, APIVersion: "v1", Kind: "Pod", Name: query.Pod},
		Container: query.Container,
		Lines:     lines,
	}, nil
}

func (r KubernetesReader) allowed(ctx context.Context, clusterID string, namespace string) bool {
	subject, _ := authctx.SubjectFrom(ctx)
	decision := r.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.resource",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: strings.TrimSpace(clusterID), Namespace: strings.TrimSpace(namespace)},
	})
	return decision.Allowed
}

func (r KubernetesReader) allowedForKind(ctx context.Context, clusterID string, namespace string, kind string) bool {
	resource := "k8s.resource"
	scope := platformrbac.Scope{ClusterID: strings.TrimSpace(clusterID), Namespace: strings.TrimSpace(namespace)}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "namespace":
		resource = "k8s.namespace"
		scope.Namespace = ""
	case "role", "rolebinding", "clusterrole", "clusterrolebinding":
		resource = "k8s.rbac"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(kind)), "cluster") {
			scope.Namespace = ""
		}
	}
	subject, _ := authctx.SubjectFrom(ctx)
	return r.authorizer.Authorize(subject, platformrbac.Request{Resource: resource, Action: "read", Scope: scope}).Allowed
}

func podLogOptions(container string) *corev1.PodLogOptions {
	tailLines := int64(200)
	limitBytes := int64(1_048_576)
	return &corev1.PodLogOptions{
		Container:  strings.TrimSpace(container),
		TailLines:  &tailLines,
		LimitBytes: &limitBytes,
	}
}

func (r KubernetesReader) listKind(ctx context.Context, client kubernetes.Interface, filter ListFilter, kind string) ([]ResourceSummary, error) {
	switch strings.ToLower(kind) {
	case "pod":
		result, err := client.CoreV1().Pods(filter.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, podSummary(filter.ClusterID, item))
		}
		return items, nil
	case "node":
		result, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for index := range result.Items {
			items = append(items, metadataSummary(filter.ClusterID, "v1", "Node", &result.Items[index]))
		}
		return items, nil
	case "deployment":
		result, err := client.AppsV1().Deployments(filter.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, deploymentSummary(filter.ClusterID, item))
		}
		return items, nil
	case "statefulset":
		result, err := client.AppsV1().StatefulSets(filter.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, statefulSetSummary(filter.ClusterID, item))
		}
		return items, nil
	case "daemonset":
		result, err := client.AppsV1().DaemonSets(filter.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, daemonSetSummary(filter.ClusterID, item))
		}
		return items, nil
	case "replicaset":
		result, err := client.AppsV1().ReplicaSets(filter.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, replicaSetSummary(filter.ClusterID, item))
		}
		return items, nil
	case "service":
		result, err := client.CoreV1().Services(filter.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, serviceSummary(filter.ClusterID, item))
		}
		return items, nil
	case "configmap":
		result, err := client.CoreV1().ConfigMaps(filter.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, configMapSummary(filter.ClusterID, item))
		}
		return items, nil
	case "namespace":
		result, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for index := range result.Items {
			items = append(items, metadataSummary(filter.ClusterID, "v1", "Namespace", &result.Items[index]))
		}
		return items, nil
	case "clusterrole":
		result, err := client.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for index := range result.Items {
			items = append(items, metadataSummary(filter.ClusterID, "rbac.authorization.k8s.io/v1", "ClusterRole", &result.Items[index]))
		}
		return items, nil
	case "clusterrolebinding":
		result, err := client.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]ResourceSummary, 0, len(result.Items))
		for index := range result.Items {
			items = append(items, metadataSummary(filter.ClusterID, rbacv1.SchemeGroupVersion.String(), "ClusterRoleBinding", &result.Items[index]))
		}
		return items, nil
	default:
		return r.listDynamicKind(ctx, filter, kind)
	}
}

func clusterScopedListKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "namespace", "node", "clusterrole", "clusterrolebinding":
		return true
	default:
		return false
	}
}

func normalizeKubernetesReadError(err error) error {
	if apierrors.IsForbidden(err) || errors.Is(err, k8sopscluster.ErrCredentialPermissionDenied) {
		return ErrReadPermissionDenied
	}
	return err
}

func (r KubernetesReader) getObject(ctx context.Context, identity Identity) (metav1.Object, Identity, error) {
	if strings.TrimSpace(identity.UID) == "" {
		return nil, Identity{}, errors.New("resource uid required")
	}
	client, err := r.clients.Clientset(ctx, identity.ClusterID)
	if err != nil {
		return nil, Identity{}, err
	}
	var object metav1.Object
	switch strings.ToLower(identity.Kind) {
	case "pod":
		object, err = client.CoreV1().Pods(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	case "deployment":
		object, err = client.AppsV1().Deployments(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	case "statefulset":
		object, err = client.AppsV1().StatefulSets(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	case "daemonset":
		object, err = client.AppsV1().DaemonSets(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	case "replicaset":
		object, err = client.AppsV1().ReplicaSets(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	case "service":
		object, err = client.CoreV1().Services(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	case "configmap":
		object, err = client.CoreV1().ConfigMaps(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	default:
		return r.getDynamicObject(ctx, identity)
	}
	if err != nil {
		return nil, Identity{}, err
	}
	actual := identityFromObject(identity.ClusterID, object)
	if actual.APIVersion != identity.APIVersion || actual.Kind != identity.Kind || actual.UID != identity.UID {
		return nil, Identity{}, errors.New("resource identity mismatch")
	}
	return object, actual, nil
}

func listKinds(kind string) []string {
	kind = strings.TrimSpace(kind)
	if kind != "" {
		return []string{kind}
	}
	return []string{
		"Pod",
		"Deployment",
		"StatefulSet",
		"DaemonSet",
		"ReplicaSet",
		"Service",
		"ConfigMap",
		"PersistentVolumeClaim",
		"PersistentVolume",
		"HorizontalPodAutoscaler",
		"Ingress",
		"Gateway",
		"VirtualService",
		"DestinationRule",
		"EnvoyFilter",
	}
}

func isKnownListKind(kind string) bool {
	for _, item := range listKinds("") {
		if strings.EqualFold(item, strings.TrimSpace(kind)) {
			return true
		}
	}
	return false
}

func (r KubernetesReader) listDynamicKind(ctx context.Context, filter ListFilter, kind string) ([]ResourceSummary, error) {
	if r.bundles == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedKind, kind)
	}
	bundle, snapshot, err := r.bundleAndSnapshot(ctx, filter.ClusterID)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveResourceVersion(snapshot, firstNonBlank(filter.APIVersion, apiVersionHint(kind)), kind)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedKind, kind)
	}
	list, err := dynamicResource(bundle, resolved, filter.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := make([]ResourceSummary, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, dynamicSummary(filter.ClusterID, resolved, item))
	}
	return items, nil
}

func (r KubernetesReader) getDynamicObject(ctx context.Context, identity Identity) (metav1.Object, Identity, error) {
	if r.bundles == nil {
		return nil, Identity{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, identity.Kind)
	}
	bundle, snapshot, err := r.bundleAndSnapshot(ctx, identity.ClusterID)
	if err != nil {
		return nil, Identity{}, err
	}
	resolved, err := resolveResourceVersion(snapshot, identity.APIVersion, identity.Kind)
	if err != nil {
		return nil, Identity{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, identity.Kind)
	}
	object, err := dynamicResource(bundle, resolved, identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
	if err != nil {
		return nil, Identity{}, err
	}
	actual := dynamicIdentity(identity.ClusterID, resolved, *object)
	if actual.Kind != identity.Kind || actual.Name != identity.Name || actual.Namespace != identity.Namespace || actual.UID != identity.UID {
		return nil, Identity{}, errors.New("resource identity mismatch")
	}
	return object, actual, nil
}

func (r KubernetesReader) bundleAndSnapshot(ctx context.Context, clusterID string) (kubeclient.Bundle, kubeclient.CapabilitySnapshot, error) {
	bundle, err := r.bundles.Bundle(ctx, clusterID)
	if err != nil {
		return kubeclient.Bundle{}, kubeclient.CapabilitySnapshot{}, err
	}
	snapshot, err := kubeclient.DiscoverCapabilities(clusterID, bundle.Discovery)
	if err != nil {
		return kubeclient.Bundle{}, kubeclient.CapabilitySnapshot{}, err
	}
	return bundle, snapshot, nil
}

func resolveResourceVersion(snapshot kubeclient.CapabilitySnapshot, apiVersion string, kind string) (kubeclient.ResolvedResourceVersion, error) {
	return kubeclient.NewResourceVersionResolver(snapshot).Resolve(kubeclient.ResourceVersionRequest{
		APIVersion: strings.TrimSpace(apiVersion),
		Kind:       strings.TrimSpace(kind),
	})
}

func dynamicResource(bundle kubeclient.Bundle, resolved kubeclient.ResolvedResourceVersion, namespace string) dynamicResourceInterface {
	resource := bundle.Dynamic.Resource(schema.GroupVersionResource{
		Group:    resolved.Group,
		Version:  resolved.Version,
		Resource: resolved.Resource,
	})
	if resolved.Namespaced {
		return resource.Namespace(namespace)
	}
	return resource
}

type dynamicResourceInterface interface {
	List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error)
	Get(ctx context.Context, name string, opts metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error)
}

func dynamicSummary(clusterID string, resolved kubeclient.ResolvedResourceVersion, item unstructured.Unstructured) ResourceSummary {
	return ResourceSummary{
		Identity:  dynamicIdentity(clusterID, resolved, item),
		Status:    statusForUnstructured(item),
		Labels:    copyLabels(item.GetLabels()),
		UpdatedAt: item.GetCreationTimestamp().Time,
	}
}

func dynamicIdentity(clusterID string, resolved kubeclient.ResolvedResourceVersion, item unstructured.Unstructured) Identity {
	return Identity{
		ClusterID:  clusterID,
		Namespace:  item.GetNamespace(),
		APIVersion: resolved.APIVersion,
		Kind:       resolved.Kind,
		Name:       item.GetName(),
		UID:        string(item.GetUID()),
	}
}

func apiVersionHint(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "persistentvolumeclaim", "persistentvolume":
		return "v1"
	case "replicaset":
		return "apps/v1"
	case "horizontalpodautoscaler":
		return "autoscaling/v2"
	case "ingress":
		return "networking.k8s.io/v1"
	case "gateway", "virtualservice", "destinationrule", "envoyfilter":
		return "networking.istio.io/v1"
	default:
		return ""
	}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func filterResourcesByQuery(items []ResourceSummary, query string) []ResourceSummary {
	filtered := make([]ResourceSummary, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Identity.Name), query) || strings.Contains(strings.ToLower(item.Status), query) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func podSummary(clusterID string, item corev1.Pod) ResourceSummary {
	return ResourceSummary{
		Identity:  Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "v1", Kind: "Pod", Name: item.Name, UID: string(item.UID)},
		Status:    podStatus(item),
		Labels:    copyLabels(item.Labels),
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func deploymentSummary(clusterID string, item appsv1.Deployment) ResourceSummary {
	return ResourceSummary{
		Identity:  Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "apps/v1", Kind: "Deployment", Name: item.Name, UID: string(item.UID)},
		Status:    deploymentStatus(item),
		Labels:    copyLabels(item.Labels),
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func statefulSetSummary(clusterID string, item appsv1.StatefulSet) ResourceSummary {
	return ResourceSummary{
		Identity:  Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "apps/v1", Kind: "StatefulSet", Name: item.Name, UID: string(item.UID)},
		Status:    statefulSetStatus(item),
		Labels:    copyLabels(item.Labels),
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func daemonSetSummary(clusterID string, item appsv1.DaemonSet) ResourceSummary {
	return ResourceSummary{
		Identity:  Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "apps/v1", Kind: "DaemonSet", Name: item.Name, UID: string(item.UID)},
		Status:    daemonSetStatus(item),
		Labels:    copyLabels(item.Labels),
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func replicaSetSummary(clusterID string, item appsv1.ReplicaSet) ResourceSummary {
	return ResourceSummary{
		Identity:  Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "apps/v1", Kind: "ReplicaSet", Name: item.Name, UID: string(item.UID)},
		Status:    replicaSetStatus(item),
		Labels:    copyLabels(item.Labels),
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func serviceSummary(clusterID string, item corev1.Service) ResourceSummary {
	return ResourceSummary{
		Identity:  Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "v1", Kind: "Service", Name: item.Name, UID: string(item.UID)},
		Status:    "active",
		Labels:    copyLabels(item.Labels),
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func configMapSummary(clusterID string, item corev1.ConfigMap) ResourceSummary {
	return ResourceSummary{
		Identity:  Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "v1", Kind: "ConfigMap", Name: item.Name, UID: string(item.UID)},
		Status:    "active",
		Labels:    copyLabels(item.Labels),
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func metadataSummary(clusterID string, apiVersion string, kind string, item metav1.Object) ResourceSummary {
	return ResourceSummary{
		Identity:  Identity{ClusterID: clusterID, Namespace: item.GetNamespace(), APIVersion: apiVersion, Kind: kind, Name: item.GetName(), UID: string(item.GetUID())},
		Status:    "active",
		Labels:    copyLabels(item.GetLabels()),
		UpdatedAt: item.GetCreationTimestamp().Time,
	}
}

func identityFromObject(clusterID string, object metav1.Object) Identity {
	switch item := object.(type) {
	case *appsv1.Deployment:
		return Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "apps/v1", Kind: "Deployment", Name: item.Name, UID: string(item.UID)}
	case *appsv1.StatefulSet:
		return Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "apps/v1", Kind: "StatefulSet", Name: item.Name, UID: string(item.UID)}
	case *appsv1.DaemonSet:
		return Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "apps/v1", Kind: "DaemonSet", Name: item.Name, UID: string(item.UID)}
	case *appsv1.ReplicaSet:
		return Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "apps/v1", Kind: "ReplicaSet", Name: item.Name, UID: string(item.UID)}
	case *corev1.Pod:
		return Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "v1", Kind: "Pod", Name: item.Name, UID: string(item.UID)}
	case *corev1.Service:
		return Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "v1", Kind: "Service", Name: item.Name, UID: string(item.UID)}
	case *corev1.ConfigMap:
		return Identity{ClusterID: clusterID, Namespace: item.Namespace, APIVersion: "v1", Kind: "ConfigMap", Name: item.Name, UID: string(item.UID)}
	default:
		return Identity{ClusterID: clusterID, Namespace: object.GetNamespace(), Name: object.GetName(), UID: string(object.GetUID())}
	}
}

func statusForObject(object metav1.Object) string {
	switch item := object.(type) {
	case *appsv1.Deployment:
		return deploymentStatus(*item)
	case *appsv1.StatefulSet:
		return statefulSetStatus(*item)
	case *appsv1.DaemonSet:
		return daemonSetStatus(*item)
	case *appsv1.ReplicaSet:
		return replicaSetStatus(*item)
	case *corev1.Pod:
		return podStatus(*item)
	default:
		return "active"
	}
}

func statusForUnstructured(item unstructured.Unstructured) string {
	phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
	if phase == "" {
		return "active"
	}
	if strings.EqualFold(phase, "running") || strings.EqualFold(phase, "succeeded") || strings.EqualFold(phase, "active") {
		return "healthy"
	}
	return "warning"
}

func podStatus(item corev1.Pod) string {
	if item.Status.Phase == corev1.PodRunning || item.Status.Phase == corev1.PodSucceeded {
		return "healthy"
	}
	if item.Status.Phase == "" {
		return "unknown"
	}
	return "warning"
}

func deploymentStatus(item appsv1.Deployment) string {
	desired := int32(1)
	if item.Spec.Replicas != nil {
		desired = *item.Spec.Replicas
	}
	if desired == 0 || item.Status.ReadyReplicas >= desired && item.Status.AvailableReplicas >= desired {
		return "healthy"
	}
	return "warning"
}

func statefulSetStatus(item appsv1.StatefulSet) string {
	desired := int32(1)
	if item.Spec.Replicas != nil {
		desired = *item.Spec.Replicas
	}
	if desired == 0 || item.Status.ReadyReplicas >= desired {
		return "healthy"
	}
	return "warning"
}

func daemonSetStatus(item appsv1.DaemonSet) string {
	desired := item.Status.DesiredNumberScheduled
	if desired == 0 || item.Status.CurrentNumberScheduled >= desired && item.Status.NumberReady >= desired {
		return "healthy"
	}
	return "warning"
}

func replicaSetStatus(item appsv1.ReplicaSet) string {
	desired := int32(1)
	if item.Spec.Replicas != nil {
		desired = *item.Spec.Replicas
	}
	if desired == 0 || item.Status.ReadyReplicas >= desired && item.Status.AvailableReplicas >= desired {
		return "healthy"
	}
	return "warning"
}

func objectSpec(object metav1.Object) (map[string]any, error) {
	content, err := objectContent(object)
	if err != nil {
		return nil, err
	}
	spec, ok := content["spec"].(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return spec, nil
}

func objectYAML(object metav1.Object, identity Identity) (string, error) {
	content, err := objectContent(object)
	if err != nil {
		return "", err
	}
	content["apiVersion"] = identity.APIVersion
	content["kind"] = identity.Kind
	if metadata, ok := content["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
	}
	data, err := yaml.Marshal(content)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(data)) + "\n", nil
}

func objectContent(object metav1.Object) (map[string]any, error) {
	if item, ok := object.(*unstructured.Unstructured); ok {
		return item.UnstructuredContent(), nil
	}
	return runtime.DefaultUnstructuredConverter.ToUnstructured(object)
}

func copyLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	copied := make(map[string]string, len(labels))
	for key, value := range labels {
		copied[key] = value
	}
	return copied
}

type allowReadAuthorizer struct{}

func (allowReadAuthorizer) Authorize(_ platformrbac.Subject, _ platformrbac.Request) platformrbac.Decision {
	return platformrbac.Decision{Allowed: true}
}
