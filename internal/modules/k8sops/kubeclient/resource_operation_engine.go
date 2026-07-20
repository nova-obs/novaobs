package kubeclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

const DefaultFieldManager = "novaapm-k8sops"

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
	Executor   string                  `json:"executor,omitempty"`
	Operation  string                  `json:"operation,omitempty"`
	BeforeHash string                  `json:"before_hash,omitempty"`
	AfterHash  string                  `json:"after_hash,omitempty"`
}

type DryRunApplyRequest struct {
	YAMLContent    string
	ForceConflicts bool
}

type ClusterDryRunApplyRequest struct {
	ClusterID      string
	YAMLContent    string
	FieldManager   string
	ForceConflicts bool
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
	result, err := engine.PreviewApply(ctx, DryRunApplyRequest{YAMLContent: req.YAMLContent, ForceConflicts: req.ForceConflicts})
	return DryRunApplyResult{Objects: result.Objects, Warnings: result.Warnings}, err
}

func (e ProviderBackedResourceOperationEngine) Apply(ctx context.Context, req ClusterApplyRequest) (ResourceOperationResult, error) {
	clusterID := strings.TrimSpace(req.ClusterID)
	if clusterID == "" {
		return ResourceOperationResult{}, ErrClusterRequired
	}
	if e.provider == nil {
		return ResourceOperationResult{}, fmt.Errorf("%w: bundle provider required", ErrResourceOperationInvalid)
	}
	bundle, err := e.provider.Bundle(ctx, clusterID)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	snapshot, err := DiscoverCapabilities(clusterID, bundle.Discovery)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	fieldManager := e.fieldManager
	if trimmed := strings.TrimSpace(req.FieldManager); trimmed != "" {
		fieldManager = trimmed
	}
	engine := NewResourceOperationEngine(bundle, snapshot, WithFieldManager(fieldManager))
	return engine.Apply(ctx, ApplyRequest{Mode: req.Mode, YAMLContent: req.YAMLContent, ForceConflicts: req.ForceConflicts})
}

func (e ProviderBackedResourceOperationEngine) Delete(ctx context.Context, req ClusterDeleteRequest) (ResourceOperationResult, error) {
	clusterID := strings.TrimSpace(req.ClusterID)
	if clusterID == "" {
		return ResourceOperationResult{}, ErrClusterRequired
	}
	if e.provider == nil {
		return ResourceOperationResult{}, fmt.Errorf("%w: bundle provider required", ErrResourceOperationInvalid)
	}
	bundle, err := e.provider.Bundle(ctx, clusterID)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	snapshot, err := DiscoverCapabilities(clusterID, bundle.Discovery)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	fieldManager := e.fieldManager
	if trimmed := strings.TrimSpace(req.FieldManager); trimmed != "" {
		fieldManager = trimmed
	}
	engine := NewResourceOperationEngine(bundle, snapshot, WithFieldManager(fieldManager))
	return engine.Delete(ctx, DeleteRequest{Mode: req.Mode, Identity: req.Identity})
}

func (e ResourceOperationEngine) DryRunApply(ctx context.Context, req DryRunApplyRequest) (DryRunApplyResult, error) {
	result, err := e.PreviewApply(ctx, req)
	return DryRunApplyResult{Objects: result.Objects, Warnings: result.Warnings}, err
}

func (e ResourceOperationEngine) PreviewApply(ctx context.Context, req DryRunApplyRequest) (PreviewApplyResult, error) {
	objects, err := decodeOperationObjects(req.YAMLContent)
	if err != nil {
		return PreviewApplyResult{}, err
	}
	resolver := NewResourceVersionResolver(e.snapshot)
	typedExecutor := newTypedOperationExecutor(e.bundle.Clientset, e.fieldManager, req.ForceConflicts)
	dynamicExecutor := newDynamicOperationExecutor(e.bundle.Dynamic, e.fieldManager, req.ForceConflicts)
	result := PreviewApplyResult{Objects: make([]OperationObject, 0, len(objects)), Warnings: append([]string{}, e.snapshot.Warnings...)}
	plannedNamespaces := namespacesPlannedByApply(objects)
	for _, object := range objects {
		resolved, err := resolver.Resolve(ResourceVersionRequest{APIVersion: object.GetAPIVersion(), Kind: object.GetKind()})
		if err != nil {
			return PreviewApplyResult{}, err
		}
		object.SetAPIVersion(resolved.APIVersion)
		object.SetKind(resolved.Kind)
		if resolved.Namespaced && object.GetNamespace() == "" {
			return PreviewApplyResult{}, ErrResourceOperationInvalid
		}
		beforeHash, operation, err := e.liveObjectDiffInput(ctx, resolved, object)
		if err != nil {
			return PreviewApplyResult{}, err
		}
		patch, err := json.Marshal(object.Object)
		if err != nil {
			return PreviewApplyResult{}, fmt.Errorf("%w: json marshal failed", ErrResourceOperationInvalid)
		}
		operationObject := operationApplyObject{
			Name:      object.GetName(),
			Namespace: object.GetNamespace(),
			Resolved:  resolved,
			Patch:     patch,
		}
		executor := OperationExecutorTyped
		handled, err := typedExecutor.Apply(ctx, operationObject, OperationModeDryRun)
		if err != nil {
			if shouldDeferDryRunForPlannedNamespace(err, resolved, object.GetNamespace(), plannedNamespaces) {
				result.Warnings = append(result.Warnings, plannedNamespaceDryRunWarning(object.GetNamespace(), resolved.Kind, object.GetName()))
				result.Objects = append(result.Objects, previewOperationObject(resolved, object, OperationExecutorDeferredDryRun, operation, beforeHash))
				continue
			}
			return PreviewApplyResult{}, resourceOperationError(err)
		}
		if !handled {
			executor = OperationExecutorDynamic
			if err := dynamicExecutor.Apply(ctx, operationObject, OperationModeDryRun); err != nil {
				if shouldDeferDryRunForPlannedNamespace(err, resolved, object.GetNamespace(), plannedNamespaces) {
					result.Warnings = append(result.Warnings, plannedNamespaceDryRunWarning(object.GetNamespace(), resolved.Kind, object.GetName()))
					result.Objects = append(result.Objects, previewOperationObject(resolved, object, OperationExecutorDeferredDryRun, operation, beforeHash))
					continue
				}
				return PreviewApplyResult{}, resourceOperationError(err)
			}
		}
		result.Objects = append(result.Objects, previewOperationObject(resolved, object, executor, operation, beforeHash))
	}
	return result, nil
}

func (e ResourceOperationEngine) Apply(ctx context.Context, req ApplyRequest) (ResourceOperationResult, error) {
	mode, err := normalizeApplyMode(req.Mode)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	objects, err := decodeOperationObjects(req.YAMLContent)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	resolver := NewResourceVersionResolver(e.snapshot)
	typedExecutor := newTypedOperationExecutor(e.bundle.Clientset, e.fieldManager, req.ForceConflicts)
	dynamicExecutor := newDynamicOperationExecutor(e.bundle.Dynamic, e.fieldManager, req.ForceConflicts)
	result := ResourceOperationResult{Objects: make([]OperationObject, 0, len(objects)), Warnings: append([]string{}, e.snapshot.Warnings...)}
	for _, object := range objects {
		resolved, err := resolver.Resolve(ResourceVersionRequest{APIVersion: object.GetAPIVersion(), Kind: object.GetKind()})
		if err != nil {
			return ResourceOperationResult{}, err
		}
		object.SetAPIVersion(resolved.APIVersion)
		object.SetKind(resolved.Kind)
		if resolved.Namespaced && object.GetNamespace() == "" {
			return ResourceOperationResult{}, ErrResourceOperationInvalid
		}
		patch, err := json.Marshal(object.Object)
		if err != nil {
			return ResourceOperationResult{}, fmt.Errorf("%w: json marshal failed", ErrResourceOperationInvalid)
		}
		operationObject := operationApplyObject{
			Name:      object.GetName(),
			Namespace: object.GetNamespace(),
			Resolved:  resolved,
			Patch:     patch,
		}
		executor := OperationExecutorTyped
		handled, err := typedExecutor.Apply(ctx, operationObject, mode)
		if err != nil {
			return ResourceOperationResult{}, resourceOperationError(err)
		}
		if !handled {
			executor = OperationExecutorDynamic
			if err := dynamicExecutor.Apply(ctx, operationObject, mode); err != nil {
				return ResourceOperationResult{}, resourceOperationError(err)
			}
		}
		result.Objects = append(result.Objects, OperationObject{
			APIVersion: resolved.APIVersion,
			Kind:       resolved.Kind,
			Namespace:  object.GetNamespace(),
			Name:       object.GetName(),
			Resolved:   resolved,
			Executor:   executor,
		})
	}
	return result, nil
}

func (e ResourceOperationEngine) Delete(ctx context.Context, req DeleteRequest) (ResourceOperationResult, error) {
	mode, err := normalizeDeleteMode(req.Mode)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	identity := normalizeOperationObject(req.Identity)
	if identity.APIVersion == "" || identity.Kind == "" || identity.Name == "" {
		return ResourceOperationResult{}, ErrResourceOperationInvalid
	}
	resolved, err := NewResourceVersionResolver(e.snapshot).Resolve(ResourceVersionRequest{APIVersion: identity.APIVersion, Kind: identity.Kind})
	if err != nil {
		return ResourceOperationResult{}, err
	}
	if resolved.Namespaced && identity.Namespace == "" {
		return ResourceOperationResult{}, ErrResourceOperationInvalid
	}
	operationObject := operationDeleteObject{
		Name:      identity.Name,
		Namespace: identity.Namespace,
		Resolved:  resolved,
	}
	typedExecutor := newTypedOperationExecutor(e.bundle.Clientset, e.fieldManager, false)
	dynamicExecutor := newDynamicOperationExecutor(e.bundle.Dynamic, e.fieldManager, false)
	executor := OperationExecutorTyped
	handled, err := typedExecutor.Delete(ctx, operationObject, mode)
	if err != nil {
		return ResourceOperationResult{}, resourceOperationError(err)
	}
	if !handled {
		executor = OperationExecutorDynamic
		if err := dynamicExecutor.Delete(ctx, operationObject, mode); err != nil {
			return ResourceOperationResult{}, resourceOperationError(err)
		}
	}
	return ResourceOperationResult{
		Objects: []OperationObject{{
			APIVersion: resolved.APIVersion,
			Kind:       resolved.Kind,
			Namespace:  identity.Namespace,
			Name:       identity.Name,
			Resolved:   resolved,
			Executor:   executor,
		}},
		Warnings: append([]string{}, e.snapshot.Warnings...),
	}, nil
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

func (e ResourceOperationEngine) liveObjectDiffInput(ctx context.Context, resolved ResolvedResourceVersion, object *unstructured.Unstructured) (string, string, error) {
	if e.bundle.Dynamic == nil {
		return "", "apply", nil
	}
	resource := e.bundle.Dynamic.Resource(gvrFromResolved(resolved))
	var live *unstructured.Unstructured
	var err error
	if resolved.Namespaced {
		live, err = resource.Namespace(object.GetNamespace()).Get(ctx, object.GetName(), metav1.GetOptions{})
	} else {
		live, err = resource.Get(ctx, object.GetName(), metav1.GetOptions{})
	}
	if apierrors.IsNotFound(err) {
		return "", "create", nil
	}
	if err != nil {
		return "", "", resourceOperationError(err)
	}
	return operationObjectHash(live), "update", nil
}

func operationObjectHash(object *unstructured.Unstructured) string {
	if object == nil {
		return ""
	}
	payload, err := json.Marshal(object.Object)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

type operationApplyObject struct {
	Name      string
	Namespace string
	Resolved  ResolvedResourceVersion
	Patch     []byte
}

type operationDeleteObject struct {
	Name      string
	Namespace string
	Resolved  ResolvedResourceVersion
}

func normalizeApplyMode(mode OperationMode) (OperationMode, error) {
	if mode == OperationModeApply || mode == OperationModeDryRun {
		return mode, nil
	}
	return "", ErrResourceOperationInvalid
}

func normalizeDeleteMode(mode OperationMode) (OperationMode, error) {
	if mode == OperationModeDelete || mode == OperationModeDryRun {
		return mode, nil
	}
	return "", ErrResourceOperationInvalid
}

func normalizeOperationObject(object OperationObject) OperationObject {
	object.APIVersion = strings.TrimSpace(object.APIVersion)
	object.Kind = strings.TrimSpace(object.Kind)
	object.Namespace = strings.TrimSpace(object.Namespace)
	object.Name = strings.TrimSpace(object.Name)
	return object
}

func resourceOperationError(err error) error {
	if apierrors.IsBadRequest(err) || apierrors.IsInvalid(err) {
		return fmt.Errorf("%w: %v", ErrResourceOperationInvalid, err)
	}
	return err
}

func namespacesPlannedByApply(objects []*unstructured.Unstructured) map[string]bool {
	out := map[string]bool{}
	for _, object := range objects {
		group, _ := parseAPIVersion(object.GetAPIVersion())
		if group == "" && strings.EqualFold(object.GetKind(), "Namespace") {
			if name := strings.TrimSpace(object.GetName()); name != "" {
				out[name] = true
			}
		}
	}
	return out
}

func shouldDeferDryRunForPlannedNamespace(err error, resolved ResolvedResourceVersion, namespace string, plannedNamespaces map[string]bool) bool {
	return resolved.Namespaced &&
		strings.TrimSpace(namespace) != "" &&
		plannedNamespaces[strings.TrimSpace(namespace)] &&
		apierrors.IsNotFound(err)
}

func plannedNamespaceDryRunWarning(namespace string, kind string, name string) string {
	return fmt.Sprintf("namespace %q 会在同一批清单中创建，已延后 %s/%s 的 server-side dry-run 校验", namespace, kind, name)
}

func previewOperationObject(resolved ResolvedResourceVersion, object *unstructured.Unstructured, executor string, operation string, beforeHash string) OperationObject {
	return OperationObject{
		APIVersion: resolved.APIVersion,
		Kind:       resolved.Kind,
		Namespace:  object.GetNamespace(),
		Name:       object.GetName(),
		Resolved:   resolved,
		Executor:   executor,
		Operation:  operation,
		BeforeHash: beforeHash,
		AfterHash:  operationObjectHash(object),
	}
}
