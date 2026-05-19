package kubeconfig

import "time"

type CreateRequest struct {
	ClusterID      string `json:"cluster_id"`
	Namespace      string `json:"namespace"`
	ServiceAccount string `json:"service_account"`
	Token          string `json:"token,omitempty"`
}

type ExportRequest struct {
	SecretID string `json:"secret_id"`
}

type CreateResult struct {
	SecretID    string    `json:"secret_id"`
	Fingerprint string    `json:"fingerprint"`
	ExpiresAt   time.Time `json:"expires_at"`
	AuditID     string    `json:"audit_id"`
}

type ExportResult struct {
	Kubeconfig string `json:"kubeconfig"`
	AuditID    string `json:"audit_id"`
}
