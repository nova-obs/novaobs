package kubeclient

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

type typedOperationExecutor struct {
	clientset    kubernetes.Interface
	fieldManager string
}

func newTypedOperationExecutor(clientset kubernetes.Interface, fieldManager string) typedOperationExecutor {
	return typedOperationExecutor{clientset: clientset, fieldManager: fieldManager}
}

func (e typedOperationExecutor) Apply(ctx context.Context, object operationApplyObject, mode OperationMode) (bool, error) {
	if e.clientset == nil {
		return false, nil
	}
	opts := metav1.PatchOptions{FieldManager: e.fieldManager}
	if mode == OperationModeDryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	switch typedResourceKey(object.Resolved) {
	case "apps/v1/deployments":
		_, err := e.clientset.AppsV1().Deployments(object.Namespace).Patch(ctx, object.Name, types.ApplyPatchType, object.Patch, opts)
		return true, err
	case "v1/configmaps":
		_, err := e.clientset.CoreV1().ConfigMaps(object.Namespace).Patch(ctx, object.Name, types.ApplyPatchType, object.Patch, opts)
		return true, err
	case "v1/services":
		_, err := e.clientset.CoreV1().Services(object.Namespace).Patch(ctx, object.Name, types.ApplyPatchType, object.Patch, opts)
		return true, err
	default:
		return false, nil
	}
}

func (e typedOperationExecutor) Delete(ctx context.Context, object operationDeleteObject, mode OperationMode) (bool, error) {
	if e.clientset == nil {
		return false, nil
	}
	opts := metav1.DeleteOptions{}
	if mode == OperationModeDryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	switch typedResourceKey(object.Resolved) {
	case "apps/v1/deployments":
		return true, e.clientset.AppsV1().Deployments(object.Namespace).Delete(ctx, object.Name, opts)
	case "v1/configmaps":
		return true, e.clientset.CoreV1().ConfigMaps(object.Namespace).Delete(ctx, object.Name, opts)
	case "v1/services":
		return true, e.clientset.CoreV1().Services(object.Namespace).Delete(ctx, object.Name, opts)
	default:
		return false, nil
	}
}

func typedResourceKey(resolved ResolvedResourceVersion) string {
	if resolved.Group == "" {
		return resolved.Version + "/" + resolved.Resource
	}
	return resolved.Group + "/" + resolved.Version + "/" + resolved.Resource
}
