package audit

import "time"

type Event struct {
	ID             string         `json:"id" bson:"_id,omitempty"`
	ActorID        string         `json:"actor_id" bson:"actor_id"`
	ActorName      string         `json:"actor_name" bson:"actor_name"`
	ResourceType   string         `json:"resource_type" bson:"resource_type"`
	ResourceName   string         `json:"resource_name" bson:"resource_name"`
	Action         string         `json:"action" bson:"action"`
	Scope          string         `json:"scope" bson:"scope"`
	RequestSummary map[string]any `json:"request_summary" bson:"request_summary"`
	Result         string         `json:"result" bson:"result"`
	ErrorMessage   string         `json:"error_message" bson:"error_message"`
	TraceID        string         `json:"trace_id" bson:"trace_id"`
	CreatedAt      time.Time      `json:"created_at" bson:"created_at"`
}
