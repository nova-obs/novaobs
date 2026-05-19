package certificate

import "time"

type Certificate struct {
	ID          string    `json:"id"`
	ClusterID   string    `json:"cluster_id"`
	Namespace   string    `json:"namespace"`
	Name        string    `json:"name"`
	CommonName  string    `json:"common_name"`
	Fingerprint string    `json:"fingerprint"`
	NotAfter    time.Time `json:"not_after"`
	Status      string    `json:"status"`
	Source      string    `json:"source"`
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

func certificateJSONTags() []string {
	return []string{"id", "cluster_id", "namespace", "name", "common_name", "fingerprint", "not_after", "status", "source"}
}
