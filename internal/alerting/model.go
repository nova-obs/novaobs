package alerting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

var (
	ErrInvalidSpec      = errors.New("invalid_alert_rule_spec")
	ErrPermissionDenied = errors.New("permission_denied")
	ErrNotFound         = errors.New("alert_not_found")
	ErrConflict         = errors.New("alert_conflict")
	ErrUnavailable      = errors.New("alert_service_unavailable")
	ErrQueryFailed      = errors.New("alert_query_failed")
	ErrTestRequired     = errors.New("alert_test_required")
)

const (
	SignalTypeLogs    = "logs"
	SignalTypeMetrics = "metrics"

	QueryModeContains  = "contains"
	QueryModeExact     = "exact"
	QueryModeLogsQL    = "logsql"
	QueryModePromQL    = "promql"
	QueryModeMetricsQL = "metricsql"

	TriggerModeWindow      = "window"
	TriggerModeConsecutive = "consecutive"

	AggregationCount = "count"
	AggregationRate  = "rate"
	OperatorGT       = "gt"
	OperatorGTE      = "gte"

	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"

	RuleStateEnabled  = "enabled"
	RuleStateDisabled = "disabled"

	ApplyStatusPending = "pending"
	ApplyStatusApplied = "applied"
	ApplyStatusFailed  = "failed"

	EvaluationHealthUnknown = "unknown"
	EvaluationHealthOK      = "ok"
	EvaluationHealthNoData  = "no_data"
	EvaluationHealthError   = "error"

	UpdateActionCreate   = "create"
	UpdateActionUpdate   = "update"
	UpdateActionDisable  = "disable"
	UpdateActionRollback = "rollback"
)

type Rule struct {
	ID              string           `json:"id" bson:"_id"`
	Spec            RuleSpec         `json:"spec" bson:"spec"`
	State           string           `json:"state" bson:"state"`
	ApplyStatus     string           `json:"apply_status" bson:"apply_status"`
	Health          EvaluationHealth `json:"health" bson:"health"`
	CurrentUpdateID string           `json:"current_update_id" bson:"current_update_id"`
	AppliedUpdateID string           `json:"applied_update_id,omitempty" bson:"applied_update_id,omitempty"`
	CreatedBy       Actor            `json:"created_by" bson:"created_by"`
	UpdatedBy       Actor            `json:"updated_by" bson:"updated_by"`
	CreatedAt       time.Time        `json:"created_at" bson:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at" bson:"updated_at"`
}

type RuleSpec struct {
	SignalType    string             `json:"signal_type" bson:"signal_type"`
	Name          string             `json:"name" bson:"name"`
	Description   string             `json:"description,omitempty" bson:"description,omitempty"`
	Scope         RuleScope          `json:"scope" bson:"scope"`
	Query         QuerySpec          `json:"query" bson:"query"`
	Trigger       TriggerSpec        `json:"trigger" bson:"trigger"`
	Grouping      GroupingSpec       `json:"grouping" bson:"grouping"`
	Notification  NotificationSpec   `json:"notification" bson:"notification"`
	DerivedMetric *DerivedMetricSpec `json:"derived_metric,omitempty" bson:"derived_metric,omitempty"`
}

type RuleScope struct {
	ServiceID       string            `json:"service_id" bson:"service_id"`
	ServiceName     string            `json:"service_name" bson:"service_name"`
	EnvironmentID   string            `json:"environment_id,omitempty" bson:"environment_id,omitempty"`
	EnvironmentName string            `json:"environment_name,omitempty" bson:"environment_name,omitempty"`
	ScopeLabels     map[string]string `json:"scope_labels,omitempty" bson:"scope_labels,omitempty"`
	LogRouteID      string            `json:"log_route_id" bson:"log_route_id"`
	LogTargetID     string            `json:"log_target_id" bson:"log_target_id"`
	EndpointID      string            `json:"endpoint_id" bson:"endpoint_id"`
	AccountID       string            `json:"account_id" bson:"account_id"`
	ProjectID       string            `json:"project_id" bson:"project_id"`
	BaseFilter      string            `json:"base_filter,omitempty" bson:"base_filter,omitempty"`
}

type QuerySpec struct {
	Mode       string `json:"mode" bson:"mode"`
	Expression string `json:"expression" bson:"expression"`
}

type TriggerSpec struct {
	Mode               string  `json:"mode" bson:"mode"`
	Aggregation        string  `json:"aggregation" bson:"aggregation"`
	Operator           string  `json:"operator" bson:"operator"`
	Threshold          float64 `json:"threshold" bson:"threshold"`
	Window             string  `json:"window" bson:"window"`
	EvaluationInterval string  `json:"evaluation_interval" bson:"evaluation_interval"`
	EvaluationDelay    string  `json:"evaluation_delay" bson:"evaluation_delay"`
	PendingFor         string  `json:"pending_for" bson:"pending_for"`
	KeepFiringFor      string  `json:"keep_firing_for" bson:"keep_firing_for"`
}

type GroupingSpec struct {
	Fields       []string `json:"fields" bson:"fields"`
	MaxInstances int      `json:"max_instances" bson:"max_instances"`
}

type NotificationSpec struct {
	PolicyID   string `json:"policy_id" bson:"policy_id"`
	Severity   string `json:"severity" bson:"severity"`
	OwnerTeam  string `json:"owner_team" bson:"owner_team"`
	RunbookURL string `json:"runbook_url,omitempty" bson:"runbook_url,omitempty"`
	Receiver   string `json:"-" bson:"-"`
}

type DerivedMetricSpec struct {
	Enabled bool              `json:"enabled" bson:"enabled"`
	Signal  string            `json:"signal,omitempty" bson:"signal,omitempty"`
	Labels  map[string]string `json:"labels,omitempty" bson:"labels,omitempty"`
}

type EvaluationHealth struct {
	Status          string    `json:"status" bson:"status"`
	LastEvaluatedAt time.Time `json:"last_evaluated_at,omitempty" bson:"last_evaluated_at,omitempty"`
	LastError       string    `json:"last_error,omitempty" bson:"last_error,omitempty"`
}

type Actor struct {
	ID   string `json:"id" bson:"id"`
	Type string `json:"type" bson:"type"`
	Name string `json:"name" bson:"name"`
}

type UpdateRecord struct {
	ID             string    `json:"id" bson:"_id"`
	RuleID         string    `json:"rule_id" bson:"rule_id"`
	ParentUpdateID string    `json:"parent_update_id,omitempty" bson:"parent_update_id,omitempty"`
	SourceUpdateID string    `json:"source_update_id,omitempty" bson:"source_update_id,omitempty"`
	Action         string    `json:"action" bson:"action"`
	ResultingState string    `json:"resulting_state" bson:"resulting_state"`
	ChangeSummary  string    `json:"change_summary" bson:"change_summary"`
	Spec           RuleSpec  `json:"spec" bson:"spec"`
	InputHash      string    `json:"input_hash" bson:"input_hash"`
	Actor          Actor     `json:"actor" bson:"actor"`
	AuditID        string    `json:"audit_id" bson:"audit_id"`
	CreatedAt      time.Time `json:"created_at" bson:"created_at"`
}

type Artifact struct {
	ID        string    `json:"id" bson:"_id"`
	RuntimeID string    `json:"runtime_id" bson:"runtime_id"`
	Hash      string    `json:"hash" bson:"hash"`
	Content   string    `json:"content" bson:"content"`
	RuleIDs   []string  `json:"rule_ids" bson:"rule_ids"`
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

type RuleFilter struct {
	ServiceID  string
	State      string
	SignalType string
}

type TestRequest struct {
	Spec       RuleSpec  `json:"spec"`
	RangeStart time.Time `json:"range_start"`
	RangeEnd   time.Time `json:"range_end"`
}

type TestResult struct {
	InputHash              string         `json:"input_hash"`
	TestToken              string         `json:"test_token,omitempty"`
	TestedAt               time.Time      `json:"tested_at"`
	CompiledQuery          string         `json:"compiled_query"`
	MatchedLogCount        int64          `json:"matched_log_count"`
	EstimatedInstanceCount int            `json:"estimated_instance_count"`
	QueryDurationMillis    int64          `json:"query_duration_ms"`
	ScannedBytes           int64          `json:"scanned_bytes,omitempty"`
	PartialResponse        bool           `json:"partial_response"`
	TopGroups              []TestTopGroup `json:"top_groups"`
	Warnings               []string       `json:"warnings"`
}

type TestTopGroup struct {
	Labels map[string]string `json:"labels"`
	Count  int64             `json:"count"`
}

func (s RuleSpec) Normalize() RuleSpec {
	s.Grouping.Fields = append([]string(nil), s.Grouping.Fields...)
	if s.DerivedMetric != nil {
		metric := *s.DerivedMetric
		metric.Labels = cloneStringMap(s.DerivedMetric.Labels)
		s.DerivedMetric = &metric
	}
	s.SignalType = strings.ToLower(strings.TrimSpace(s.SignalType))
	if s.SignalType == "" {
		s.SignalType = SignalTypeLogs
	}
	s.Name = strings.TrimSpace(s.Name)
	s.Description = strings.TrimSpace(s.Description)
	s.Scope.ServiceID = strings.TrimSpace(s.Scope.ServiceID)
	s.Scope.ServiceName = strings.TrimSpace(s.Scope.ServiceName)
	s.Scope.EnvironmentID = strings.TrimSpace(s.Scope.EnvironmentID)
	s.Scope.EnvironmentName = strings.TrimSpace(s.Scope.EnvironmentName)
	s.Scope.ScopeLabels = cloneStringMap(s.Scope.ScopeLabels)
	for key, value := range s.Scope.ScopeLabels {
		trimmedKey, trimmedValue := strings.TrimSpace(key), strings.TrimSpace(value)
		delete(s.Scope.ScopeLabels, key)
		if trimmedKey != "" {
			s.Scope.ScopeLabels[trimmedKey] = trimmedValue
		}
	}
	s.Scope.LogRouteID = strings.TrimSpace(s.Scope.LogRouteID)
	s.Scope.LogTargetID = strings.TrimSpace(s.Scope.LogTargetID)
	s.Scope.EndpointID = strings.TrimSpace(s.Scope.EndpointID)
	s.Scope.AccountID = strings.TrimSpace(s.Scope.AccountID)
	s.Scope.ProjectID = strings.TrimSpace(s.Scope.ProjectID)
	s.Scope.BaseFilter = strings.TrimSpace(s.Scope.BaseFilter)
	s.Query.Mode = strings.ToLower(strings.TrimSpace(s.Query.Mode))
	s.Query.Expression = strings.TrimSpace(s.Query.Expression)
	if s.Trigger.Mode == "" {
		s.Trigger.Mode = TriggerModeWindow
	}
	if s.Trigger.Aggregation == "" {
		s.Trigger.Aggregation = AggregationCount
	}
	if s.Trigger.Operator == "" {
		s.Trigger.Operator = OperatorGTE
	}
	if s.Trigger.EvaluationInterval == "" {
		s.Trigger.EvaluationInterval = "30s"
	}
	if s.Trigger.EvaluationDelay == "" {
		s.Trigger.EvaluationDelay = "0s"
	}
	if s.Trigger.PendingFor == "" {
		s.Trigger.PendingFor = "0s"
	}
	if s.Trigger.KeepFiringFor == "" {
		s.Trigger.KeepFiringFor = "0s"
	}
	if s.Grouping.MaxInstances == 0 {
		s.Grouping.MaxInstances = 100
	}
	for i := range s.Grouping.Fields {
		s.Grouping.Fields[i] = strings.TrimSpace(s.Grouping.Fields[i])
	}
	slices.Sort(s.Grouping.Fields)
	s.Notification.PolicyID = strings.TrimSpace(s.Notification.PolicyID)
	s.Notification.OwnerTeam = strings.TrimSpace(s.Notification.OwnerTeam)
	s.Notification.RunbookURL = strings.TrimSpace(s.Notification.RunbookURL)
	s.Notification.Severity = strings.ToLower(strings.TrimSpace(s.Notification.Severity))
	return s
}

func (s RuleSpec) Validate() error {
	s = s.Normalize()
	if s.Name == "" {
		return invalidSpec("name", "规则名称不能为空")
	}
	if len(s.Name) > 120 {
		return invalidSpec("name", "规则名称不能超过 120 个字符")
	}
	if !slices.Contains([]string{SignalTypeLogs, SignalTypeMetrics}, s.SignalType) {
		return invalidSpec("signal_type", "告警信号类型无效")
	}
	if err := validateScopeForSignal(s); err != nil {
		return err
	}
	if err := validateQueryForSignal(s); err != nil {
		return err
	}
	if s.Trigger.Mode != TriggerModeWindow {
		return invalidSpec("trigger.mode", "一期不支持严格连续事件匹配，仅支持时间窗口内累计匹配")
	}
	if !slices.Contains([]string{AggregationCount, AggregationRate}, s.Trigger.Aggregation) {
		return invalidSpec("trigger.aggregation", "聚合方式无效")
	}
	if !slices.Contains([]string{OperatorGT, OperatorGTE}, s.Trigger.Operator) {
		return invalidSpec("trigger.operator", "条件操作符无效")
	}
	if s.Trigger.Threshold <= 0 {
		return invalidSpec("trigger.threshold", "触发阈值必须大于 0")
	}
	window, err := validateDuration("trigger.window", s.Trigger.Window, time.Second, time.Hour)
	if err != nil {
		return err
	}
	interval, err := validateDuration("trigger.evaluation_interval", s.Trigger.EvaluationInterval, time.Second, window)
	if err != nil {
		return err
	}
	if interval > window {
		return invalidSpec("trigger.evaluation_interval", "评估间隔不能大于时间窗口")
	}
	for field, value := range map[string]string{
		"trigger.evaluation_delay": s.Trigger.EvaluationDelay,
		"trigger.pending_for":      s.Trigger.PendingFor,
		"trigger.keep_firing_for":  s.Trigger.KeepFiringFor,
	} {
		if _, err := validateDuration(field, value, 0, time.Hour); err != nil {
			return err
		}
	}
	if len(s.Grouping.Fields) > 3 {
		return invalidSpec("grouping.fields", "告警分组字段最多 3 个")
	}
	seen := map[string]bool{}
	for _, field := range s.Grouping.Fields {
		if field == "" || seen[field] {
			return invalidSpec("grouping.fields", "告警分组字段不能为空或重复")
		}
		seen[field] = true
	}
	if s.Grouping.MaxInstances < 1 || s.Grouping.MaxInstances > 100 {
		return invalidSpec("grouping.max_instances", "单条规则最多允许 100 个告警实例")
	}
	if s.Notification.PolicyID == "" || s.Notification.OwnerTeam == "" {
		return invalidSpec("notification", "通知策略和责任团队不能为空")
	}
	if !slices.Contains([]string{SeverityInfo, SeverityWarning, SeverityCritical}, s.Notification.Severity) {
		return invalidSpec("notification.severity", "严重程度无效")
	}
	if s.Notification.RunbookURL != "" {
		parsed, err := url.ParseRequestURI(s.Notification.RunbookURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return invalidSpec("notification.runbook_url", "Runbook URL 必须是有效的 HTTP(S) 地址")
		}
	}
	if metric := s.DerivedMetric; metric != nil && metric.Enabled {
		if s.SignalType == SignalTypeMetrics {
			return invalidSpec("derived_metric", "指标告警不支持日志派生指标")
		}
		if !slices.Contains([]string{"match_count", "match_rate"}, metric.Signal) {
			return invalidSpec("derived_metric.signal", "趋势指标类型无效")
		}
		if len(metric.Labels) > 4 {
			return invalidSpec("derived_metric.labels", "趋势指标最多允许 4 个静态标签")
		}
		if len(s.Grouping.Fields) > 0 {
			return invalidSpec("derived_metric", "一期趋势指标只生成规则级聚合，不继承告警分组字段，避免高基数时序膨胀")
		}
		allowedLabels := []string{"environment", "region", "team", "component"}
		for key, value := range metric.Labels {
			if !slices.Contains(allowedLabels, key) || strings.TrimSpace(value) == "" || len(value) > 64 {
				return invalidSpec("derived_metric.labels", "趋势指标只允许受控的低基数静态标签")
			}
		}
	}
	return nil
}

func validateScopeForSignal(s RuleSpec) error {
	switch s.SignalType {
	case SignalTypeLogs:
		if s.Scope.ServiceID == "" || s.Scope.ServiceName == "" || s.Scope.EndpointID == "" || (s.Scope.LogRouteID == "" && s.Scope.LogTargetID == "") {
			return invalidSpec("scope", "服务、日志目标或日志路由、日志端点不能为空")
		}
		if s.Scope.LogRouteID != "" && s.Scope.LogTargetID != "" {
			return invalidSpec("scope", "日志目标和日志路由不能同时绑定")
		}
		if s.Scope.AccountID == "" || s.Scope.ProjectID == "" {
			return invalidSpec("scope", "VictoriaLogs AccountID 和 ProjectID 不能为空")
		}
	case SignalTypeMetrics:
		if s.Scope.EnvironmentID == "" || s.Scope.EnvironmentName == "" || s.Scope.EndpointID == "" {
			return invalidSpec("scope", "环境和指标写入目标不能为空")
		}
		if s.Scope.ServiceID != "" || s.Scope.ServiceName != "" || s.Scope.LogRouteID != "" || s.Scope.LogTargetID != "" || s.Scope.BaseFilter != "" {
			return invalidSpec("scope", "指标告警只能绑定环境，不能绑定服务或日志采集对象")
		}
		for key, value := range s.Scope.ScopeLabels {
			if !validMetricScopeLabel(key, value) {
				return invalidSpec("scope.scope_labels", "指标作用域标签必须是合法的低基数标签")
			}
		}
	default:
		return invalidSpec("signal_type", "告警信号类型无效")
	}
	return nil
}

func validateQueryForSignal(s RuleSpec) error {
	if s.Query.Expression == "" || len(s.Query.Expression) > 16*1024 {
		return invalidSpec("query.expression", "查询内容不能为空且不能超过 16 KiB")
	}
	switch s.SignalType {
	case SignalTypeLogs:
		if !slices.Contains([]string{QueryModeContains, QueryModeExact, QueryModeLogsQL}, s.Query.Mode) {
			return invalidSpec("query.mode", "查询模式无效")
		}
		if s.Query.Mode == QueryModeLogsQL && (strings.Contains(s.Query.Expression, "|") || strings.Contains(strings.ToLower(s.Query.Expression), "_time")) {
			return invalidSpec("query.expression", "LogsQL 高级模式仅接受过滤表达式，时间窗口和统计由平台统一生成")
		}
		if s.Query.Mode == QueryModeLogsQL && !balancedLogsQLFilter(s.Query.Expression) {
			return invalidSpec("query.expression", "LogsQL 过滤表达式括号或引号不完整")
		}
	case SignalTypeMetrics:
		if !slices.Contains([]string{QueryModePromQL, QueryModeMetricsQL}, s.Query.Mode) {
			return invalidSpec("query.mode", "查询模式无效")
		}
		if strings.Contains(s.Query.Expression, "$") {
			return invalidSpec("query.expression", "PromQL/MetricsQL 表达式不能包含 Dashboard 变量")
		}
		if !balancedMetricQuery(s.Query.Expression) {
			return invalidSpec("query.expression", "PromQL/MetricsQL 表达式括号或引号不完整")
		}
	default:
		return invalidSpec("signal_type", "告警信号类型无效")
	}
	return nil
}

func balancedLogsQLFilter(expression string) bool {
	depth := 0
	quoted := false
	escaped := false
	for _, char := range expression {
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quoted {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch char {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return !quoted && !escaped && depth == 0
}

func balancedMetricQuery(expression string) bool {
	stack := []rune{}
	quoted := false
	escaped := false
	for _, char := range expression {
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quoted {
			escaped = true
			continue
		}
		if char == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch char {
		case '(':
			stack = append(stack, ')')
		case '[':
			stack = append(stack, ']')
		case '{':
			stack = append(stack, '}')
		case ')', ']', '}':
			if len(stack) == 0 || stack[len(stack)-1] != char {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return !quoted && !escaped && len(stack) == 0
}

func (s RuleSpec) InputHash() (string, error) {
	normalized := s.Normalize()
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("计算告警规则输入 hash: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type validationError struct {
	Field   string
	Message string
}

func invalidSpec(field string, message string) error {
	return validationError{Field: field, Message: message}
}

func validMetricScopeLabel(key string, value string) bool {
	allowed := map[string]struct{}{
		"cluster": {}, "namespace": {}, "region": {}, "team": {}, "component": {},
	}
	_, ok := allowed[key]
	return ok && value != "" && len(value) <= 64
}

func (e validationError) Error() string { return e.Field + ": " + e.Message }
func (e validationError) Unwrap() error { return ErrInvalidSpec }

func validateDuration(field string, raw string, min time.Duration, max time.Duration) (time.Duration, error) {
	value, err := time.ParseDuration(raw)
	if err != nil || value < min || value > max {
		return 0, invalidSpec(field, fmt.Sprintf("持续时间必须在 %s 到 %s 之间", min, max))
	}
	return value, nil
}
