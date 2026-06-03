package cluster

type Cluster struct {
	ID          string `json:"id" bson:"_id,omitempty"`
	Name        string `json:"name" bson:"name"`
	Version     string `json:"version" bson:"version"`
	Region      string `json:"region" bson:"region"`
	Description string `json:"description" bson:"description"`
	Status      string `json:"status" bson:"status"`
	AccessMode  string `json:"access_mode" bson:"access_mode"`
	ReadOnly    bool   `json:"read_only" bson:"read_only"`
	ReadOnlySet bool   `json:"read_only_configured" bson:"read_only_configured"`
}

type UpsertRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Region      string `json:"region"`
	Description string `json:"description"`
	Status      string `json:"status"`
	AccessMode  string `json:"access_mode"`
	ReadOnly    bool   `json:"read_only"`
}

type ListFilter struct {
	Query    string
	Page     int
	PageSize int
	Sort     string
	Order    string
}
