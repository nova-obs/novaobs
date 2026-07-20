package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"novaapm/internal/modules/k8sops/kubeclient"
)

type PreviewPlan struct {
	ID                string
	ConfirmationToken string
	Resources         []ResourceIdentity
	Diffs             []ResourceDiff
	Warnings          []string
}

type ResourceDiff struct {
	ClusterID  string `json:"cluster_id"`
	Namespace  string `json:"namespace,omitempty"`
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Operation  string `json:"operation"`
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash"`
}

func buildPreviewPlan(clusterID string, identities []ResourceIdentity, warnings []string) PreviewPlan {
	resources := cloneAndSortIdentities(clusterID, identities)
	diffs := make([]ResourceDiff, 0, len(resources))
	for _, resource := range resources {
		diffs = append(diffs, ResourceDiff{
			ClusterID:  resource.ClusterID,
			Namespace:  resource.Namespace,
			APIVersion: resource.APIVersion,
			Kind:       resource.Kind,
			Name:       resource.Name,
			Operation:  "apply",
			AfterHash:  resourceAfterHash(resource),
		})
	}
	source := previewPlanSource(diffs)
	id := shortDigest("preview:" + source)
	token := digest("confirm:" + source)
	return PreviewPlan{
		ID:                id,
		ConfirmationToken: token,
		Resources:         resources,
		Diffs:             diffs,
		Warnings:          append([]string{}, warnings...),
	}
}

func buildPreviewPlanFromOperationObjects(clusterID string, objects []kubeclient.OperationObject, warnings []string) PreviewPlan {
	resources := make([]ResourceIdentity, 0, len(objects))
	diffs := make([]ResourceDiff, 0, len(objects))
	for _, object := range objects {
		identity := normalizeIdentity(ResourceIdentity{
			ClusterID:  clusterID,
			Namespace:  object.Namespace,
			APIVersion: object.APIVersion,
			Kind:       object.Kind,
			Name:       object.Name,
		})
		resources = append(resources, identity)
		operation := strings.TrimSpace(object.Operation)
		if operation == "" {
			operation = "apply"
		}
		afterHash := strings.TrimSpace(object.AfterHash)
		if afterHash == "" {
			afterHash = resourceAfterHash(identity)
		}
		diffs = append(diffs, ResourceDiff{
			ClusterID:  identity.ClusterID,
			Namespace:  identity.Namespace,
			APIVersion: identity.APIVersion,
			Kind:       identity.Kind,
			Name:       identity.Name,
			Operation:  operation,
			BeforeHash: strings.TrimSpace(object.BeforeHash),
			AfterHash:  afterHash,
		})
	}
	resources = cloneAndSortIdentities(clusterID, resources)
	sort.SliceStable(diffs, func(left, right int) bool {
		return diffSortKey(diffs[left]) < diffSortKey(diffs[right])
	})
	source := previewPlanSource(diffs)
	return PreviewPlan{
		ID:                shortDigest("preview:" + source),
		ConfirmationToken: digest("confirm:" + source),
		Resources:         resources,
		Diffs:             diffs,
		Warnings:          append([]string{}, warnings...),
	}
}

func buildDeletePlan(identity ResourceIdentity) PreviewPlan {
	identity = normalizeIdentity(identity)
	diff := ResourceDiff{
		ClusterID:  identity.ClusterID,
		Namespace:  identity.Namespace,
		APIVersion: identity.APIVersion,
		Kind:       identity.Kind,
		Name:       identity.Name,
		Operation:  "delete",
		BeforeHash: digest("before:" + identitySortKey(identity) + "/" + identity.UID),
	}
	source := previewPlanSource([]ResourceDiff{diff})
	return PreviewPlan{
		ID:                shortDigest("delete-preview:" + source),
		ConfirmationToken: digest("delete-confirm:" + source),
		Resources:         []ResourceIdentity{identity},
		Diffs:             []ResourceDiff{diff},
	}
}

func cloneAndSortIdentities(clusterID string, identities []ResourceIdentity) []ResourceIdentity {
	out := make([]ResourceIdentity, 0, len(identities))
	for _, identity := range identities {
		identity.ClusterID = strings.TrimSpace(identity.ClusterID)
		if identity.ClusterID == "" {
			identity.ClusterID = strings.TrimSpace(clusterID)
		}
		out = append(out, normalizeIdentity(identity))
	}
	sort.SliceStable(out, func(left, right int) bool {
		return identitySortKey(out[left]) < identitySortKey(out[right])
	})
	return out
}

func previewPlanSource(diffs []ResourceDiff) string {
	parts := make([]string, 0, len(diffs))
	for _, diff := range diffs {
		parts = append(parts, strings.Join([]string{
			diff.ClusterID,
			diff.Namespace,
			diff.APIVersion,
			diff.Kind,
			diff.Name,
			diff.Operation,
			diff.BeforeHash,
			diff.AfterHash,
		}, "\x00"))
	}
	return strings.Join(parts, "\x1f")
}

func resourceAfterHash(identity ResourceIdentity) string {
	return digest("after:" + identitySortKey(identity))
}

func identitySortKey(identity ResourceIdentity) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", identity.ClusterID, identity.Namespace, identity.Kind, identity.Name, identity.APIVersion)
}

func diffSortKey(diff ResourceDiff) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", diff.ClusterID, diff.Namespace, diff.Kind, diff.Name, diff.APIVersion)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func shortDigest(value string) string {
	return digest(value)[:24]
}
