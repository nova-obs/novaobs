package namespace

import "time"

type Namespace struct {
	ID        string    `json:"id" bson:"_id,omitempty"`
	ClusterID string    `json:"cluster_id" bson:"cluster_id"`
	Name      string    `json:"name" bson:"name"`
	Status    string    `json:"status" bson:"status"`
	Owner     string    `json:"owner" bson:"owner"`
	Phase     string    `json:"phase" bson:"phase"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

type ListFilter struct {
	ClusterID string
	Query     string
	Page      int
	PageSize  int
	Sort      string
	Order     string
}

type CreateRequest struct {
	ClusterID string `json:"cluster_id"`
	Name      string `json:"name"`
	Owner     string `json:"owner,omitempty"`
}

type DeleteRequest struct {
	ClusterID string
	Name      string
	UID       string
}
