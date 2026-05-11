package logquery

import "time"

type Query struct {
	Service     string
	Environment string
	Level       string
	Keyword     string
	TraceID     string
	RequestID   string
	Start       *time.Time
	End         *time.Time
}

type Result struct {
	Items []LogEntry `json:"items"`
	Total int        `json:"total"`
}

type LogEntry struct {
	Timestamp       time.Time         `json:"timestamp"`
	Level           string            `json:"level"`
	Message         string            `json:"message"`
	Tenant          string            `json:"tenant"`
	Service         string            `json:"service"`
	Environment     string            `json:"environment"`
	Cluster         string            `json:"cluster"`
	Namespace       string            `json:"namespace"`
	Pod             string            `json:"pod"`
	Container       string            `json:"container"`
	TraceID         string            `json:"trace_id"`
	SpanID          string            `json:"span_id"`
	RequestID       string            `json:"request_id"`
	ErrorCode       string            `json:"error_code"`
	PipelineID      string            `json:"pipeline_id"`
	PipelineVersion int               `json:"pipeline_version"`
	ParseStatus     string            `json:"parse_status"`
	CMDBMatchStatus string            `json:"cmdb_match_status"`
	Labels          map[string]string `json:"labels"`
}
