package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	obsendpoint "novaapm/internal/observability/endpoint"
)

type HealthVerifier interface {
	Verify(ctx context.Context, destination obsendpoint.Endpoint, environmentID string, sources []SourceAccess, observedAt time.Time) (HealthLayer, HealthLayer, []SourceHealth, []EnvironmentSignal)
}

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type VictoriaMetricsHealthVerifier struct{ client HTTPDoer }

func NewVictoriaMetricsHealthVerifier(client HTTPDoer) VictoriaMetricsHealthVerifier {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return VictoriaMetricsHealthVerifier{client: client}
}

func (v VictoriaMetricsHealthVerifier) Verify(ctx context.Context, destination obsendpoint.Endpoint, environmentID string, sources []SourceAccess, observedAt time.Time) (HealthLayer, HealthLayer, []SourceHealth, []EnvironmentSignal) {
	if _, err := v.query(ctx, destination.URLs.QueryURL, "vector(1)"); err != nil {
		failed := HealthLayer{Status: HealthFailed, Message: "VictoriaMetrics 查询失败: " + err.Error(), ObservedAt: observedAt}
		return failed, HealthLayer{Status: HealthUnknown, Message: "目标不可查询，未验证数据流", ObservedAt: observedAt}, unknownSourceHealth(sources, "目标不可查询"), nil
	}
	destinationHealth := HealthLayer{Status: HealthHealthy, Message: "VictoriaMetrics 查询端点连通", ObservedAt: observedAt}
	selector := "{" + EnvironmentIdentityLabel + "=" + strconv.Quote(environmentID) + "}"
	result, err := v.query(ctx, destination.URLs.QueryURL, "time() - max(timestamp("+selector+"))")
	dataFlow := HealthLayer{Status: HealthUnknown, Message: "尚未发现该环境的指标样本", ObservedAt: observedAt}
	if err != nil {
		dataFlow = HealthLayer{Status: HealthFailed, Message: "数据新鲜度查询失败: " + err.Error(), ObservedAt: observedAt}
	} else if age, ok := result.scalar(); ok {
		if age <= 600 {
			dataFlow = HealthLayer{Status: HealthHealthy, Message: "最近样本在 10 分钟内", ObservedAt: observedAt}
		} else {
			dataFlow = HealthLayer{Status: HealthDegraded, Message: fmt.Sprintf("最近样本距今 %.0f 秒", age), ObservedAt: observedAt}
		}
	}
	sourceHealth := make([]SourceHealth, 0, len(sources))
	for _, source := range sources {
		sourceHealth = append(sourceHealth, v.verifySource(ctx, destination.URLs.QueryURL, environmentID, source))
	}
	return destinationHealth, dataFlow, sourceHealth, v.verifyEnvironmentSignals(ctx, destination.URLs.QueryURL, environmentID)
}

func (v VictoriaMetricsHealthVerifier) verifyEnvironmentSignals(ctx context.Context, queryURL string, environmentID string) []EnvironmentSignal {
	matcher := EnvironmentIdentityLabel + "=" + strconv.Quote(environmentID)
	definitions := []struct {
		key, label, unit, query string
		degraded                func(float64) bool
	}{
		{"cpu_utilization", "CPU 使用率", "ratio", `1 - avg(rate(node_cpu_seconds_total{` + matcher + `,mode="idle"}[5m]))`, func(value float64) bool { return value >= .85 }},
		{"memory_utilization", "内存使用率", "ratio", `1 - sum(node_memory_MemAvailable_bytes{` + matcher + `}) / sum(node_memory_MemTotal_bytes{` + matcher + `})`, func(value float64) bool { return value >= .85 }},
		{"nodes_not_ready", "非 Ready 节点", "count", `sum(kube_node_status_condition{` + matcher + `,condition="Ready",status="true"} == 0)`, func(value float64) bool { return value > 0 }},
		{"deployment_unavailable_replicas", "不可用 Deployment 副本", "count", `sum(kube_deployment_status_replicas_unavailable{` + matcher + `})`, func(value float64) bool { return value > 0 }},
	}
	items := make([]EnvironmentSignal, 0, len(definitions))
	for _, definition := range definitions {
		result, err := v.query(ctx, queryURL, definition.query)
		if err != nil {
			continue
		}
		value, ok := result.scalar()
		if !ok {
			continue
		}
		status := HealthHealthy
		if definition.degraded(value) {
			status = HealthDegraded
		}
		items = append(items, EnvironmentSignal{Key: definition.key, Label: definition.label, Value: value, Unit: definition.unit, Status: status})
	}
	return items
}

func (v VictoriaMetricsHealthVerifier) verifySource(ctx context.Context, queryURL string, environmentID string, source SourceAccess) SourceHealth {
	required := requiredMetrics(source.SourceKind)
	if len(required) == 0 {
		return SourceHealth{SourceAccessID: source.ID, SourceKind: source.SourceKind, Status: HealthUnknown, Message: "该来源尚未配置可验证信号"}
	}
	found := 0
	for _, metricName := range required {
		query := "count(" + metricName + "{" + EnvironmentIdentityLabel + "=" + strconv.Quote(environmentID) + "})"
		result, err := v.query(ctx, queryURL, query)
		if err == nil && result.hasPositiveValue() {
			found++
		}
	}
	status := HealthFailed
	if found == len(required) {
		status = HealthHealthy
	} else if found > 0 {
		status = HealthDegraded
	}
	return SourceHealth{SourceAccessID: source.ID, SourceKind: source.SourceKind, Status: status, Message: fmt.Sprintf("已验证 %d/%d 个关键信号", found, len(required))}
}

func requiredMetrics(sourceKind string) []string {
	switch sourceKind {
	case SourceKindKubernetesInfra:
		return []string{"node_cpu_seconds_total", "kube_node_status_condition", "container_cpu_usage_seconds_total"}
	case SourceKindHostInfra:
		return []string{"node_cpu_seconds_total", "node_memory_MemAvailable_bytes"}
	case SourceKindLogDerived:
		return []string{"novaapm_log_matches", "novaapm_log_match_rate"}
	default:
		return nil
	}
}

func unknownSourceHealth(sources []SourceAccess, message string) []SourceHealth {
	items := make([]SourceHealth, 0, len(sources))
	for _, source := range sources {
		items = append(items, SourceHealth{SourceAccessID: source.ID, SourceKind: source.SourceKind, Status: HealthUnknown, Message: message})
	}
	return items
}

type vmQueryResult struct {
	Data struct {
		Result []struct {
			Value []any `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func (r vmQueryResult) scalar() (float64, bool) {
	if len(r.Data.Result) == 0 || len(r.Data.Result[0].Value) < 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(fmt.Sprint(r.Data.Result[0].Value[1]), 64)
	return value, err == nil
}

func (r vmQueryResult) hasPositiveValue() bool { value, ok := r.scalar(); return ok && value > 0 }

func (v VictoriaMetricsHealthVerifier) query(ctx context.Context, rawURL string, expression string) (vmQueryResult, error) {
	queryURL, err := buildMetricsQueryURL(rawURL, expression)
	if err != nil {
		return vmQueryResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		return vmQueryResult{}, err
	}
	response, err := v.client.Do(request)
	if err != nil {
		return vmQueryResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return vmQueryResult{}, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return vmQueryResult{}, fmt.Errorf("响应格式无效")
	}
	if envelope.Status != "success" {
		return vmQueryResult{}, fmt.Errorf("查询未成功")
	}
	return vmQueryResult{Data: envelope.Data}, nil
}

func buildMetricsQueryURL(rawURL string, expression string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("查询地址无效")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, "/api/v1/query") {
		path += "/api/v1/query"
	}
	parsed.Path = path
	values := parsed.Query()
	values.Set("query", expression)
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}
