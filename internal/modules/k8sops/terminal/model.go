package terminal

type ExecRequest struct {
	ClusterID string `json:"cluster_id"`
	Namespace string `json:"namespace"`
	Command   string `json:"command"`
}

type ExecResult struct {
	Status          string   `json:"status"`
	ClusterID       string   `json:"cluster_id"`
	Namespace       string   `json:"namespace"`
	Command         string   `json:"command"`
	Verb            string   `json:"verb"`
	Args            []string `json:"args"`
	Output          string   `json:"output"`
	ExitCode        int      `json:"exit_code"`
	AuditID         string   `json:"audit_id,omitempty"`
	BlockedReason   string   `json:"blocked_reason,omitempty"`
	Mode            string   `json:"mode"`
	OutputTruncated bool     `json:"output_truncated"`
}
