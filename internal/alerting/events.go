package alerting

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var alertMetadataKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	AlertStatePending  = "pending"
	AlertStateFiring   = "firing"
	AlertStateResolved = "resolved"
)

type AlertInstance struct {
	Fingerprint     string            `json:"fingerprint" bson:"_id"`
	RuleID          string            `json:"rule_id" bson:"rule_id"`
	ServiceID       string            `json:"service_id" bson:"service_id"`
	State           string            `json:"state" bson:"state"`
	Labels          map[string]string `json:"labels" bson:"labels"`
	Annotations     map[string]string `json:"annotations" bson:"annotations"`
	StartsAt        time.Time         `json:"starts_at" bson:"starts_at"`
	EndsAt          time.Time         `json:"ends_at,omitempty" bson:"ends_at,omitempty"`
	LastReceivedAt  time.Time         `json:"last_received_at" bson:"last_received_at"`
	LastEventID     string            `json:"last_event_id" bson:"last_event_id"`
	SourceRuntimeID string            `json:"source_runtime_id,omitempty" bson:"source_runtime_id,omitempty"`
}

type AlertEvent struct {
	ID              string            `json:"id" bson:"_id"`
	Fingerprint     string            `json:"fingerprint" bson:"fingerprint"`
	RuleID          string            `json:"rule_id" bson:"rule_id"`
	ServiceID       string            `json:"service_id" bson:"service_id"`
	PreviousState   string            `json:"previous_state,omitempty" bson:"previous_state,omitempty"`
	State           string            `json:"state" bson:"state"`
	Labels          map[string]string `json:"labels" bson:"labels"`
	Annotations     map[string]string `json:"annotations" bson:"annotations"`
	SourceRuntimeID string            `json:"source_runtime_id,omitempty" bson:"source_runtime_id,omitempty"`
	OccurredAt      time.Time         `json:"occurred_at" bson:"occurred_at"`
	ReceivedAt      time.Time         `json:"received_at" bson:"received_at"`
}

type AlertInstanceFilter struct {
	RuleID    string
	ServiceID string
	State     string
	Limit     int
}

type AlertEventFilter struct {
	RuleID      string
	Fingerprint string
	Limit       int
}

type AlertIngestAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

type EventRepository interface {
	ApplyEvent(ctx context.Context, instance AlertInstance, event AlertEvent) error
	ListInstances(ctx context.Context, filter AlertInstanceFilter) ([]AlertInstance, error)
	ListEvents(ctx context.Context, filter AlertEventFilter) ([]AlertEvent, error)
}

type EventRuleResolver interface {
	GetRule(ctx context.Context, id string) (Rule, error)
}

type EventIngestor struct {
	repository EventRepository
	rules      EventRuleResolver
	tokenHash  [32]byte
	clock      func() time.Time
}

func NewEventIngestor(repository EventRepository, rules EventRuleResolver, token string, clock func() time.Time) EventIngestor {
	if clock == nil {
		clock = time.Now
	}
	return EventIngestor{repository: repository, rules: rules, tokenHash: sha256.Sum256([]byte(token)), clock: clock}
}

func (i EventIngestor) IngestAlerts(ctx context.Context, token string, alerts []AlertIngestAlert) (int, error) {
	providedHash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(i.tokenHash[:], providedHash[:]) != 1 || strings.TrimSpace(token) == "" {
		return 0, ErrPermissionDenied
	}
	if i.repository == nil {
		return 0, ErrUnavailable
	}
	receivedAt := i.clock().UTC()
	for _, alert := range alerts {
		instance, event, err := normalizeAlertIngestEvent(alert, receivedAt)
		if err != nil {
			return 0, err
		}
		if err := i.validateRule(ctx, instance); err != nil {
			return 0, err
		}
		if err := i.repository.ApplyEvent(ctx, instance, event); err != nil {
			return 0, err
		}
	}
	return len(alerts), nil
}

func (i EventIngestor) validateRule(ctx context.Context, instance AlertInstance) error {
	if i.rules == nil {
		return ErrUnavailable
	}
	rule, err := i.rules.GetRule(ctx, instance.RuleID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return invalidSpec("labels.novaapm_rule_id", "告警事件引用的规则不存在")
		}
		return err
	}
	if rule.State != RuleStateEnabled {
		return invalidSpec("labels.novaapm_rule_id", "告警事件引用的规则未启用")
	}
	if rule.Spec.Scope.ServiceID != instance.ServiceID {
		return invalidSpec("labels.service_id", "告警事件服务身份与规则范围不一致")
	}
	return nil
}

func normalizeAlertIngestEvent(alert AlertIngestAlert, receivedAt time.Time) (AlertInstance, AlertEvent, error) {
	ruleID := strings.TrimSpace(alert.Labels["novaapm_rule_id"])
	serviceID := strings.TrimSpace(alert.Labels["service_id"])
	if ruleID == "" || serviceID == "" || len(ruleID) > 128 || len(serviceID) > 128 {
		return AlertInstance{}, AlertEvent{}, invalidSpec("labels", "告警事件缺少规则或服务身份")
	}
	if !validAlertMetadata(alert.Labels, 128) || !validAlertMetadata(alert.Annotations, 64) {
		return AlertInstance{}, AlertEvent{}, invalidSpec("metadata", "告警事件标签或注解无效")
	}
	state := AlertStateFiring
	status := strings.TrimSpace(alert.Status)
	if status == "" && !alert.EndsAt.IsZero() && !alert.EndsAt.After(receivedAt) {
		state = AlertStateResolved
	} else if status == "" || strings.EqualFold(status, AlertStateFiring) {
		state = AlertStateFiring
	} else if strings.EqualFold(status, AlertStateResolved) {
		state = AlertStateResolved
	} else {
		return AlertInstance{}, AlertEvent{}, invalidSpec("status", "告警接入状态无效")
	}
	fingerprint := strings.TrimSpace(alert.Fingerprint)
	if len(fingerprint) > 256 {
		return AlertInstance{}, AlertEvent{}, invalidSpec("fingerprint", "告警指纹过长")
	}
	if fingerprint == "" {
		fingerprint = derivedFingerprint(ruleID, alert.Labels)
	}
	occurredAt := alert.StartsAt.UTC()
	if state == AlertStateResolved && !alert.EndsAt.IsZero() {
		occurredAt = alert.EndsAt.UTC()
	}
	if occurredAt.IsZero() {
		occurredAt = receivedAt
	}
	eventID := eventIdentity(fingerprint, state, alert.StartsAt, alert.EndsAt)
	runtimeID := strings.TrimSpace(alert.Labels["novaapm_runtime_id"])
	instance := AlertInstance{
		Fingerprint: fingerprint, RuleID: ruleID, ServiceID: serviceID, State: state,
		Labels: cloneStringMap(alert.Labels), Annotations: cloneStringMap(alert.Annotations),
		StartsAt: alert.StartsAt.UTC(), EndsAt: alert.EndsAt.UTC(), LastReceivedAt: receivedAt,
		LastEventID: eventID, SourceRuntimeID: runtimeID,
	}
	event := AlertEvent{
		ID: eventID, Fingerprint: fingerprint, RuleID: ruleID, ServiceID: serviceID, State: state,
		Labels: cloneStringMap(alert.Labels), Annotations: cloneStringMap(alert.Annotations),
		SourceRuntimeID: runtimeID, OccurredAt: occurredAt, ReceivedAt: receivedAt,
	}
	return instance, event, nil
}

func validAlertMetadata(values map[string]string, maxItems int) bool {
	if len(values) > maxItems {
		return false
	}
	for key, value := range values {
		if !alertMetadataKeyPattern.MatchString(key) || len(value) > 4096 {
			return false
		}
	}
	return true
}

func derivedFingerprint(ruleID string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if key != "alertname" && key != "severity" && key != "notification_policy_id" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var value strings.Builder
	value.WriteString(ruleID)
	for _, key := range keys {
		fmt.Fprintf(&value, "\x00%s\x00%s", key, labels[key])
	}
	sum := sha256.Sum256([]byte(value.String()))
	return hex.EncodeToString(sum[:])
}

func eventIdentity(fingerprint string, state string, startsAt time.Time, endsAt time.Time) string {
	raw := fingerprint + "\x00" + state + "\x00" + startsAt.UTC().Format(time.RFC3339Nano) + "\x00" + endsAt.UTC().Format(time.RFC3339Nano)
	sum := sha256.Sum256([]byte(raw))
	return "event-" + hex.EncodeToString(sum[:16])
}

func cloneStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
