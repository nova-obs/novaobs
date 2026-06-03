package deployment

import "time"

type HistoryRecord struct {
	ID         string    `json:"id" bson:"_id,omitempty"`
	ClusterID  string    `json:"cluster_id" bson:"cluster_id"`
	Namespace  string    `json:"namespace" bson:"namespace"`
	Workload   string    `json:"workload" bson:"workload"`
	Action     string    `json:"action" bson:"action"`
	Status     string    `json:"status" bson:"status"`
	Revision   string    `json:"revision" bson:"revision"`
	Actor      string    `json:"actor" bson:"actor"`
	StartedAt  time.Time `json:"started_at" bson:"started_at"`
	FinishedAt time.Time `json:"finished_at" bson:"finished_at"`
}

type AuditEvent struct {
	ID           string    `json:"id"`
	ClusterID    string    `json:"cluster_id"`
	Namespace    string    `json:"namespace"`
	ResourceKind string    `json:"resource_kind"`
	ResourceName string    `json:"resource_name"`
	Action       string    `json:"action"`
	Actor        string    `json:"actor"`
	Status       string    `json:"status"`
	TraceID      string    `json:"trace_id"`
	CreatedAt    time.Time `json:"created_at"`
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
