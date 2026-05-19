package deployment

type OperationRequest struct {
	ClusterID   string `json:"cluster_id"`
	YAMLContent string `json:"yaml_content"`
}

type ResourceIdentity struct {
	ClusterID  string `json:"cluster_id"`
	Namespace  string `json:"namespace"`
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
}

type DeleteRequest struct {
	Identity ResourceIdentity `json:"identity"`
}

type RollbackRequest struct {
	Identity  ResourceIdentity `json:"identity"`
	HistoryID string           `json:"history_id"`
}

type OperationResult struct {
	Status    string             `json:"status"`
	Message   string             `json:"message"`
	AuditID   string             `json:"audit_id"`
	Resources []ResourceIdentity `json:"resources"`
}
