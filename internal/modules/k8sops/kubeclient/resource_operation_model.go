package kubeclient

type OperationMode string

const (
	OperationModeDryRun OperationMode = "dry_run"
	OperationModeApply  OperationMode = "apply"
	OperationModeDelete OperationMode = "delete"
)

const (
	OperationExecutorTyped          = "typed"
	OperationExecutorDynamic        = "dynamic"
	OperationExecutorDeferredDryRun = "deferred_dry_run"
)

type ApplyRequest struct {
	Mode           OperationMode
	YAMLContent    string
	ForceConflicts bool
}

type DeleteRequest struct {
	Mode     OperationMode
	Identity OperationObject
}

type ClusterApplyRequest struct {
	ClusterID      string
	Mode           OperationMode
	YAMLContent    string
	FieldManager   string
	ForceConflicts bool
}

type ClusterDeleteRequest struct {
	ClusterID    string
	Mode         OperationMode
	Identity     OperationObject
	FieldManager string
}

type ResourceOperationResult struct {
	Objects  []OperationObject `json:"objects"`
	Warnings []string          `json:"warnings"`
}

type PreviewApplyResult = ResourceOperationResult
