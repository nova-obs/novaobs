package certificate

import "time"

type Certificate struct {
	ID          string    `json:"id"`
	ClusterID   string    `json:"cluster_id"`
	Namespace   string    `json:"namespace"`
	Name        string    `json:"name"`
	CommonName  string    `json:"common_name"`
	Fingerprint string    `json:"fingerprint"`
	SecretID    string    `json:"secret_id,omitempty"`
	NotAfter    time.Time `json:"not_after"`
	Status      string    `json:"status"`
	Source      string    `json:"source"`
	Certificate string    `json:"-"`
	PrivateKey  string    `json:"-"`
}

type ListFilter struct {
	ClusterID string
	Namespace string
	Query     string
	Page      int
	PageSize  int
	Sort      string
	Order     string
}

type CreateRequest struct {
	ClusterID      string `json:"cluster_id"`
	Namespace      string `json:"namespace"`
	Name           string `json:"name"`
	CommonName     string `json:"common_name"`
	CertificatePEM string `json:"certificate_pem"`
	PrivateKeyPEM  string `json:"private_key_pem"`
	NotAfter       string `json:"not_after"`
}

type DeleteRequest struct {
	ID        string
	ClusterID string
	Namespace string
	Name      string
}

func certificateJSONTags() []string {
	return []string{"id", "cluster_id", "namespace", "name", "common_name", "fingerprint", "secret_id", "not_after", "status", "source"}
}
