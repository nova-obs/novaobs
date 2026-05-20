package kubeclient

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

type dynamicOperationExecutor struct {
	client       dynamic.Interface
	fieldManager string
}

func newDynamicOperationExecutor(client dynamic.Interface, fieldManager string) dynamicOperationExecutor {
	return dynamicOperationExecutor{client: client, fieldManager: fieldManager}
}

func (e dynamicOperationExecutor) Apply(ctx context.Context, object operationApplyObject, mode OperationMode) error {
	if e.client == nil {
		return fmt.Errorf("%w: dynamic client required", ErrResourceOperationInvalid)
	}
	opts := metav1.PatchOptions{FieldManager: e.fieldManager}
	if mode == OperationModeDryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	resource := e.client.Resource(gvrFromResolved(object.Resolved))
	if object.Resolved.Namespaced {
		_, err := resource.Namespace(object.Namespace).Patch(ctx, object.Name, types.ApplyPatchType, object.Patch, opts)
		return err
	}
	_, err := resource.Patch(ctx, object.Name, types.ApplyPatchType, object.Patch, opts)
	return err
}

func (e dynamicOperationExecutor) Delete(ctx context.Context, object operationDeleteObject, mode OperationMode) error {
	if e.client == nil {
		return fmt.Errorf("%w: dynamic client required", ErrResourceOperationInvalid)
	}
	opts := metav1.DeleteOptions{}
	if mode == OperationModeDryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	resource := e.client.Resource(gvrFromResolved(object.Resolved))
	if object.Resolved.Namespaced {
		return resource.Namespace(object.Namespace).Delete(ctx, object.Name, opts)
	}
	return resource.Delete(ctx, object.Name, opts)
}

func gvrFromResolved(resolved ResolvedResourceVersion) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: resolved.Group, Version: resolved.Version, Resource: resolved.Resource}
}
