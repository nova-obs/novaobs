package serviceaccount

import "time"

type ServiceAccount struct {
	ID        string    `json:"id"`
	ClusterID string    `json:"cluster_id"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	UID       string    `json:"uid"`
	Status    string    `json:"status"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

type ListFilter struct {
	ClusterID string
	Namespace string
	Query     string
	Page      int
	PageSize  int
}

type CreateRequest struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Token     string `json:"token,omitempty"`
}

type DeleteRequest struct {
	ClusterID string
	Namespace string
	Name      string
	UID       string
}
