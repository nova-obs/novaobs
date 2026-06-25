package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxVictoriaLogsResponseBytes = 2 << 20

type QueryTarget struct {
	QueryURL  string
	AccountID string
	ProjectID string
}

type QueryTargetResolver interface {
	ResolveQueryTarget(ctx context.Context, scope RuleScope) (QueryTarget, error)
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type VictoriaLogsTester struct {
	resolver QueryTargetResolver
	client   HTTPDoer
}

func NewVictoriaLogsTester(resolver QueryTargetResolver, client HTTPDoer) VictoriaLogsTester {
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	return VictoriaLogsTester{resolver: resolver, client: client}
}

func (t VictoriaLogsTester) Test(ctx context.Context, req TestRequest) (TestResult, error) {
	if t.resolver == nil || t.client == nil {
		return TestResult{}, ErrUnavailable
	}
	target, err := t.resolver.ResolveQueryTarget(ctx, req.Spec.Scope)
	if err != nil {
		return TestResult{}, err
	}
	if target.AccountID != req.Spec.Scope.AccountID || target.ProjectID != req.Spec.Scope.ProjectID {
		return TestResult{}, fmt.Errorf("%w: VictoriaLogs 租户与规则范围不一致", ErrQueryFailed)
	}
	query, err := CompileTestQuery(req.Spec)
	if err != nil {
		return TestResult{}, err
	}
	endpoint, err := safeQueryURL(target.QueryURL)
	if err != nil {
		return TestResult{}, err
	}
	form := url.Values{
		"query":                  []string{query},
		"start":                  []string{req.RangeStart.UTC().Format(time.RFC3339Nano)},
		"end":                    []string{req.RangeEnd.UTC().Format(time.RFC3339Nano)},
		"timeout":                []string{"10s"},
		"allow_partial_response": []string{"0"},
	}
	queryCtx, cancel := context.WithTimeout(ctx, 11*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(queryCtx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TestResult{}, fmt.Errorf("%w: 创建 VictoriaLogs 请求失败", ErrQueryFailed)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("AccountID", target.AccountID)
	httpReq.Header.Set("ProjectID", target.ProjectID)
	started := time.Now()
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return TestResult{}, fmt.Errorf("%w: VictoriaLogs 查询失败", ErrQueryFailed)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return TestResult{}, fmt.Errorf("%w: VictoriaLogs 返回 HTTP %d", ErrQueryFailed, resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxVictoriaLogsResponseBytes+1))
	if err != nil || len(payload) > maxVictoriaLogsResponseBytes {
		return TestResult{}, fmt.Errorf("%w: VictoriaLogs 响应过大或读取失败", ErrQueryFailed)
	}
	result, err := parseStatsRows(payload, req.Spec.Grouping.Fields)
	if err != nil {
		return TestResult{}, err
	}
	result.CompiledQuery = query
	result.QueryDurationMillis = queryDurationMillis(resp.Header.Get("VL-Request-Duration-Seconds"), time.Since(started))
	result.PartialResponse = strings.EqualFold(resp.Header.Get("VL-Partial-Response"), "true")
	return result, nil
}

func safeQueryURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: VictoriaLogs QueryURL 无效", ErrQueryFailed)
	}
	return parsed.String(), nil
}

func parseStatsRows(payload []byte, groupFields []string) (TestResult, error) {
	result := TestResult{}
	lines := bytes.Split(payload, []byte("\n"))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row map[string]any
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&row); err != nil {
			return TestResult{}, fmt.Errorf("%w: VictoriaLogs 统计响应格式无效", ErrQueryFailed)
		}
		count, err := numericValue(row["matches"])
		if err != nil {
			return TestResult{}, fmt.Errorf("%w: VictoriaLogs 统计值无效", ErrQueryFailed)
		}
		result.MatchedLogCount += int64(count)
		labels := map[string]string{}
		for _, field := range groupFields {
			if value, ok := row[field]; ok {
				labels[field] = fmt.Sprint(value)
			}
		}
		if count > 0 {
			result.EstimatedInstanceCount++
			result.TopGroups = append(result.TopGroups, TestTopGroup{Labels: labels, Count: int64(count)})
		}
	}
	sort.Slice(result.TopGroups, func(i, j int) bool { return result.TopGroups[i].Count > result.TopGroups[j].Count })
	if len(result.TopGroups) > 10 {
		result.TopGroups = result.TopGroups[:10]
	}
	return result, nil
}

func numericValue(value any) (float64, error) {
	switch typed := value.(type) {
	case json.Number:
		return typed.Float64()
	case string:
		return strconv.ParseFloat(typed, 64)
	case float64:
		return typed, nil
	default:
		return 0, fmt.Errorf("unsupported number %T", value)
	}
}

func queryDurationMillis(raw string, fallback time.Duration) int64 {
	seconds, err := strconv.ParseFloat(raw, 64)
	if err == nil && seconds >= 0 {
		return int64(seconds * 1000)
	}
	return fallback.Milliseconds()
}
