package cluster

type Cluster struct {
	ID          string `json:"id" bson:"_id,omitempty"`
	Name        string `json:"name" bson:"name"`
	Version     string `json:"version" bson:"version"`
	Region      string `json:"region" bson:"region"`
	Description string `json:"description" bson:"description"`
	Status      string `json:"status" bson:"status"`
}

type UpsertRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Region      string `json:"region"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type ListFilter struct {
	Query    string
	Page     int
	PageSize int
	Sort     string
	Order    string
}
