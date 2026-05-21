package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
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

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func shortDigest(value string) string {
	return digest(value)[:24]
}
