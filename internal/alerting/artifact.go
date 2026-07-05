package alerting

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var alertNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_:]`)

type vmalertDocument struct {
	Groups []vmalertGroup `yaml:"groups"`
}

type vmalertGroup struct {
	Name      string        `yaml:"name"`
	Type      string        `yaml:"type"`
	Interval  string        `yaml:"interval"`
	EvalDelay string        `yaml:"eval_delay,omitempty"`
	Limit     int           `yaml:"limit"`
	Headers   []string      `yaml:"headers"`
	Rules     []vmalertRule `yaml:"rules"`
}

type vmalertRule struct {
	Alert         string            `yaml:"alert,omitempty"`
	Record        string            `yaml:"record,omitempty"`
	Expr          string            `yaml:"expr"`
	For           string            `yaml:"for,omitempty"`
	KeepFiringFor string            `yaml:"keep_firing_for,omitempty"`
	Labels        map[string]string `yaml:"labels,omitempty"`
	Annotations   map[string]string `yaml:"annotations,omitempty"`
}

func CompileVmalertArtifact(runtimeID string, rules []Rule, createdAt time.Time) (Artifact, error) {
	enabled := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.State == RuleStateEnabled {
			enabled = append(enabled, rule)
		}
	}
	slices.SortFunc(enabled, func(a, b Rule) int { return strings.Compare(a.ID, b.ID) })
	document := vmalertDocument{Groups: []vmalertGroup{}}
	ruleIDs := make([]string, 0, len(enabled))
	for _, rule := range enabled {
		if !notificationReceiverPattern.MatchString(rule.Spec.Notification.Receiver) {
			return Artifact{}, fmt.Errorf("规则 %s 未解析到有效的通知 receiver", rule.ID)
		}
		expr, err := CompileAlertQuery(rule.Spec)
		if err != nil {
			return Artifact{}, fmt.Errorf("编译告警规则 %s: %w", rule.ID, err)
		}
		pendingFor := omitZeroDuration(rule.Spec.Trigger.PendingFor)
		keepFiringFor := omitZeroDuration(rule.Spec.Trigger.KeepFiringFor)
		annotations := map[string]string{
			"summary":     rule.Spec.Name,
			"description": rule.Spec.Description,
		}
		if rule.Spec.Notification.RunbookURL != "" {
			annotations["runbook_url"] = rule.Spec.Notification.RunbookURL
		}
		runtimeRules := []vmalertRule{{
			Alert:         "NovaObsLogAlert_" + safeAlertName(rule.ID),
			Expr:          expr,
			For:           pendingFor,
			KeepFiringFor: keepFiringFor,
			Labels: map[string]string{
				"novaobs_rule_id":        rule.ID,
				"novaobs_runtime_id":     runtimeID,
				"service_id":             rule.Spec.Scope.ServiceID,
				"severity":               rule.Spec.Notification.Severity,
				"owner_team":             rule.Spec.Notification.OwnerTeam,
				"notification_policy_id": rule.Spec.Notification.PolicyID,
				"notification_receiver":  rule.Spec.Notification.Receiver,
			},
			Annotations: annotations,
		}}
		if metric := rule.Spec.DerivedMetric; metric != nil && metric.Enabled {
			recordingQuery, err := CompileRecordingQuery(rule.Spec)
			if err != nil {
				return Artifact{}, fmt.Errorf("编译趋势指标 %s: %w", rule.ID, err)
			}
			labels := map[string]string{"novaobs_rule_id": rule.ID, "service_id": rule.Spec.Scope.ServiceID}
			for key, value := range metric.Labels {
				labels[key] = value
			}
			recordName := "novaobs_log_matches"
			if metric.Signal == "match_rate" {
				recordName = "novaobs_log_match_rate"
			}
			runtimeRules = append(runtimeRules, vmalertRule{Record: recordName, Expr: recordingQuery, Labels: labels})
		}
		document.Groups = append(document.Groups, vmalertGroup{
			Name:      "novaobs_log_" + safeAlertName(rule.ID),
			Type:      "vlogs",
			Interval:  rule.Spec.Trigger.EvaluationInterval,
			EvalDelay: omitZeroDuration(rule.Spec.Trigger.EvaluationDelay),
			Limit:     rule.Spec.Grouping.MaxInstances,
			Headers: []string{
				"AccountID: " + rule.Spec.Scope.AccountID,
				"ProjectID: " + rule.Spec.Scope.ProjectID,
			},
			Rules: runtimeRules,
		})
		ruleIDs = append(ruleIDs, rule.ID)
	}
	content, err := yaml.Marshal(document)
	if err != nil {
		return Artifact{}, fmt.Errorf("序列化 vmalert artifact: %w", err)
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	return Artifact{
		ID: "artifact-" + hash[:16], RuntimeID: runtimeID, Hash: hash,
		Content: string(content), RuleIDs: ruleIDs, CreatedAt: createdAt.UTC(),
	}, nil
}

func safeAlertName(value string) string {
	value = alertNameSanitizer.ReplaceAllString(value, "_")
	if value == "" || (value[0] >= '0' && value[0] <= '9') {
		return "rule_" + value
	}
	return value
}

func omitZeroDuration(value string) string {
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed == 0 {
		return ""
	}
	return value
}
