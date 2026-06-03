package audit

import "time"

type Actor struct {
	ID   string `json:"id" bson:"id"`
	Name string `json:"name" bson:"name"`
}

type Resource struct {
	Type string `json:"type" bson:"type"`
	Name string `json:"name" bson:"name"`
}

type Event struct {
	ID             string         `json:"id" bson:"_id,omitempty"`
	Actor          Actor          `json:"actor" bson:"actor"`
	ActorID        string         `json:"actor_id" bson:"actor_id"`
	ActorName      string         `json:"actor_name" bson:"actor_name"`
	Resource       Resource       `json:"resource" bson:"resource"`
	ResourceType   string         `json:"resource_type" bson:"resource_type"`
	ResourceName   string         `json:"resource_name" bson:"resource_name"`
	Action         string         `json:"action" bson:"action"`
	Scope          string         `json:"scope" bson:"scope"`
	RequestSummary map[string]any `json:"request_summary" bson:"request_summary"`
	Result         string         `json:"result" bson:"result"`
	Error          string         `json:"error" bson:"error"`
	ErrorMessage   string         `json:"error_message" bson:"error_message"`
	Trace          string         `json:"trace" bson:"trace"`
	TraceID        string         `json:"trace_id" bson:"trace_id"`
	CreatedAt      time.Time      `json:"created_at" bson:"created_at"`
}
