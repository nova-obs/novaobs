package kubeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

const DefaultFieldManager = "novaobs-k8sops"

var ErrResourceOperationInvalid = errors.New("k8s_resource_operation_invalid")

type ResourceOperationEngine struct {
	bundle       Bundle
	snapshot     CapabilitySnapshot
	fieldManager string
}

type ResourceOperationOption func(*ResourceOperationEngine)

type BundleProvider interface {
	Bundle(ctx context.Context, clusterID string) (Bundle, error)
}

type ProviderBackedResourceOperationEngine struct {
	provider     BundleProvider
	fieldManager string
}

type OperationObject struct {
	APIVersion string                  `json:"api_version"`
	Kind       string                  `json:"kind"`
	Namespace  string                  `json:"namespace,omitempty"`
	Name       string                  `json:"name"`
	Resolved   ResolvedResourceVersion `json:"resolved"`
}

type DryRunApplyRequest struct {
	YAMLContent string
}

type ClusterDryRunApplyRequest struct {
	ClusterID    string
	YAMLContent  string
	FieldManager string
}

type DryRunApplyResult struct {
	Objects  []OperationObject `json:"objects"`
	Warnings []string          `json:"warnings"`
}

func NewResourceOperationEngine(bundle Bundle, snapshot CapabilitySnapshot, opts ...ResourceOperationOption) ResourceOperationEngine {
	engine := ResourceOperationEngine{
		bundle:       bundle,
		snapshot:     snapshot,
		fieldManager: DefaultFieldManager,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&engine)
		}
	}
	return engine
}

func WithFieldManager(fieldManager string) ResourceOperationOption {
	return func(engine *ResourceOperationEngine) {
		if trimmed := strings.TrimSpace(fieldManager); trimmed != "" {
			engine.fieldManager = trimmed
		}
	}
}

func NewProviderBackedResourceOperationEngine(provider BundleProvider, opts ...ResourceOperationOption) ProviderBackedResourceOperationEngine {
	engine := NewResourceOperationEngine(Bundle{}, CapabilitySnapshot{}, opts...)
	return ProviderBackedResourceOperationEngine{provider: provider, fieldManager: engine.fieldManager}
}

func (e ProviderBackedResourceOperationEngine) DryRunApply(ctx context.Context, req ClusterDryRunApplyRequest) (DryRunApplyResult, error) {
	clusterID := strings.TrimSpace(req.ClusterID)
	if clusterID == "" {
		return DryRunApplyResult{}, ErrClusterRequired
	}
	if e.provider == nil {
		return DryRunApplyResult{}, fmt.Errorf("%w: bundle provider required", ErrResourceOperationInvalid)
	}
	bundle, err := e.provider.Bundle(ctx, clusterID)
	if err != nil {
		return DryRunApplyResult{}, err
	}
	snapshot, err := DiscoverCapabilities(clusterID, bundle.Discovery)
	if err != nil {
		return DryRunApplyResult{}, err
	}
	fieldManager := e.fieldManager
	if trimmed := strings.TrimSpace(req.FieldManager); trimmed != "" {
		fieldManager = trimmed
	}
	engine := NewResourceOperationEngine(bundle, snapshot, WithFieldManager(fieldManager))
	return engine.DryRunApply(ctx, DryRunApplyRequest{YAMLContent: req.YAMLContent})
}

func (e ResourceOperationEngine) DryRunApply(ctx context.Context, req DryRunApplyRequest) (DryRunApplyResult, error) {
	if e.bundle.Dynamic == nil {
		return DryRunApplyResult{}, fmt.Errorf("%w: dynamic client required", ErrResourceOperationInvalid)
	}
	objects, err := decodeOperationObjects(req.YAMLContent)
	if err != nil {
		return DryRunApplyResult{}, err
	}
	resolver := NewResourceVersionResolver(e.snapshot)
	result := DryRunApplyResult{Objects: make([]OperationObject, 0, len(objects)), Warnings: append([]string{}, e.snapshot.Warnings...)}
	for _, object := range objects {
		resolved, err := resolver.Resolve(ResourceVersionRequest{APIVersion: object.GetAPIVersion(), Kind: object.GetKind()})
		if err != nil {
			return DryRunApplyResult{}, err
		}
		object.SetAPIVersion(resolved.APIVersion)
		object.SetKind(resolved.Kind)
		if resolved.Namespaced && object.GetNamespace() == "" {
			return DryRunApplyResult{}, ErrResourceOperationInvalid
		}
		patch, err := json.Marshal(object.Object)
		if err != nil {
			return DryRunApplyResult{}, fmt.Errorf("%w: json marshal failed", ErrResourceOperationInvalid)
		}
		gvr := schema.GroupVersionResource{Group: resolved.Group, Version: resolved.Version, Resource: resolved.Resource}
		resource := e.bundle.Dynamic.Resource(gvr)
		var patchErr error
		if resolved.Namespaced {
			_, patchErr = resource.Namespace(object.GetNamespace()).Patch(ctx, object.GetName(), types.ApplyPatchType, patch, metav1.PatchOptions{
				FieldManager: e.fieldManager,
				DryRun:       []string{metav1.DryRunAll},
			})
		} else {
			_, patchErr = resource.Patch(ctx, object.GetName(), types.ApplyPatchType, patch, metav1.PatchOptions{
				FieldManager: e.fieldManager,
				DryRun:       []string{metav1.DryRunAll},
			})
		}
		if patchErr != nil {
			return DryRunApplyResult{}, resourceOperationError(patchErr)
		}
		result.Objects = append(result.Objects, OperationObject{
			APIVersion: resolved.APIVersion,
			Kind:       resolved.Kind,
			Namespace:  object.GetNamespace(),
			Name:       object.GetName(),
			Resolved:   resolved,
		})
	}
	return result, nil
}

func decodeOperationObjects(yamlContent string) ([]*unstructured.Unstructured, error) {
	yamlContent = strings.TrimSpace(yamlContent)
	if yamlContent == "" {
		return nil, ErrResourceOperationInvalid
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(yamlContent)), 4096)
	objects := []*unstructured.Unstructured{}
	for {
		var raw map[string]any
		err := decoder.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: yaml parse failed", ErrResourceOperationInvalid)
		}
		if len(raw) == 0 {
			continue
		}
		object := &unstructured.Unstructured{Object: raw}
		if object.GetAPIVersion() == "" || object.GetKind() == "" || object.GetName() == "" {
			return nil, ErrResourceOperationInvalid
		}
		objects = append(objects, object)
	}
	if len(objects) == 0 {
		return nil, ErrResourceOperationInvalid
	}
	return objects, nil
}

func resourceOperationError(err error) error {
	if apierrors.IsBadRequest(err) || apierrors.IsInvalid(err) {
		return fmt.Errorf("%w: %v", ErrResourceOperationInvalid, err)
	}
	return err
}
