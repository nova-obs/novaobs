package alerting

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var logsQLFieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

func CompileTestQuery(spec RuleSpec) (string, error) {
	return compileQuery(spec, false, false)
}

func CompileAlertQuery(spec RuleSpec) (string, error) {
	return compileQuery(spec, true, true)
}

func CompileRecordingQuery(spec RuleSpec) (string, error) {
	if spec.DerivedMetric != nil && spec.DerivedMetric.Signal == "match_rate" {
		spec.Trigger.Aggregation = AggregationRate
	} else {
		spec.Trigger.Aggregation = AggregationCount
	}
	return compileQuery(spec, true, false)
}

func compileQuery(spec RuleSpec, applyTimeWindow bool, applyThreshold bool) (string, error) {
	spec = spec.Normalize()
	if err := spec.Validate(); err != nil {
		return "", err
	}
	if spec.SignalType == SignalTypeMetrics {
		return compileMetricQuery(spec, applyThreshold)
	}
	filter, err := compileFilter(spec.Query)
	if err != nil {
		return "", err
	}
	scopeFilter := strconv.Quote("service.name") + ":=" + strconv.Quote(spec.Scope.ServiceName)
	if spec.Scope.BaseFilter != "" {
		scopeFilter = "(" + spec.Scope.BaseFilter + ")"
	}
	query := scopeFilter + " AND (" + filter + ")"
	if applyTimeWindow {
		query = "_time:" + spec.Trigger.Window + " AND " + query
	}
	grouping := ""
	if len(spec.Grouping.Fields) > 0 {
		for _, field := range spec.Grouping.Fields {
			if !logsQLFieldPattern.MatchString(field) {
				return "", invalidSpec("grouping.fields", "告警分组字段格式无效")
			}
		}
		grouping = " by (" + strings.Join(spec.Grouping.Fields, ", ") + ")"
	}
	function := "count()"
	if spec.Trigger.Aggregation == AggregationRate {
		function = "rate()"
	}
	query += " | stats" + grouping + " " + function + " as matches"
	if applyThreshold {
		op := ">="
		if spec.Trigger.Operator == OperatorGT {
			op = ">"
		}
		query += " | filter matches:" + op + strconv.FormatFloat(spec.Trigger.Threshold, 'f', -1, 64)
		fields := append([]string{}, spec.Grouping.Fields...)
		fields = append(fields, "matches")
		query += " | fields " + strings.Join(fields, ", ")
	}
	return query, nil
}

func compileMetricQuery(spec RuleSpec, applyThreshold bool) (string, error) {
	expression := strings.TrimSpace(spec.Query.Expression)
	if spec.Scope.BasePromQL != "" {
		expression = "(" + expression + ") and (" + spec.Scope.BasePromQL + ")"
	}
	if applyThreshold {
		op := ">="
		if spec.Trigger.Operator == OperatorGT {
			op = ">"
		}
		expression = "(" + expression + ") " + op + " " + strconv.FormatFloat(spec.Trigger.Threshold, 'f', -1, 64)
	}
	return expression, nil
}

func compileFilter(query QuerySpec) (string, error) {
	switch query.Mode {
	case QueryModeContains:
		return strconv.Quote(query.Expression), nil
	case QueryModeExact:
		return strconv.Quote("_msg") + ":=" + strconv.Quote(query.Expression), nil
	case QueryModeLogsQL:
		return query.Expression, nil
	default:
		return "", fmt.Errorf("%w: query.mode", ErrInvalidSpec)
	}
}
