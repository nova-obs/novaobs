package metrics

import (
	"time"

	obsendpoint "novaapm/internal/observability/endpoint"
	"novaapm/internal/servicecatalog"
)

const (
	BindingStatusActive              = "active"
	BindingStatusPendingVerification = "pending_verification"
	BindingStatusDisabled            = "disabled"

	ProbeStatusPending  = "pending"
	ProbeStatusVerified = "verified"
	ProbeStatusFailed   = "failed"
)

type ActorRef struct {
	ID   string `json:"id" bson:"id"`
	Type string `json:"type" bson:"type"`
	Name string `json:"name" bson:"name"`
}

type ServiceBinding struct {
	ID               string                     `json:"id" bson:"_id"`
	ServiceID        string                     `json:"service_id" bson:"service_id"`
	EndpointID       string                     `json:"endpoint_id" bson:"endpoint_id"`
	Tenant           obsendpoint.EndpointTenant `json:"tenant" bson:"tenant"`
	LabelMatch       map[string]string          `json:"label_match" bson:"label_match"`
	BasePromQL       string                     `json:"base_promql" bson:"base_promql"`
	Status           string                     `json:"status" bson:"status"`
	LastProbeStatus  string                     `json:"last_probe_status" bson:"last_probe_status"`
	LastProbeMessage string                     `json:"last_probe_message" bson:"last_probe_message"`
	LastProbeAt      *time.Time                 `json:"last_probe_at,omitempty" bson:"last_probe_at,omitempty"`
	CreatedBy        ActorRef                   `json:"created_by" bson:"created_by"`
	UpdatedBy        ActorRef                   `json:"updated_by" bson:"updated_by"`
	CreatedAt        time.Time                  `json:"created_at" bson:"created_at"`
	UpdatedAt        time.Time                  `json:"updated_at" bson:"updated_at"`
}

type ServiceBindingView struct {
	Binding  ServiceBinding          `json:"binding"`
	Service  *servicecatalog.Service `json:"service,omitempty"`
	Endpoint *obsendpoint.Endpoint   `json:"endpoint,omitempty"`
}

type CreateServiceBindingRequest struct {
	ServiceID  string                     `json:"service_id"`
	EndpointID string                     `json:"endpoint_id"`
	Tenant     obsendpoint.EndpointTenant `json:"tenant"`
	LabelMatch map[string]string          `json:"label_match"`
	BasePromQL string                     `json:"base_promql"`
	Status     string                     `json:"status"`
}

type UpdateServiceBindingRequest struct {
	EndpointID string                     `json:"endpoint_id"`
	Tenant     obsendpoint.EndpointTenant `json:"tenant"`
	LabelMatch map[string]string          `json:"label_match"`
	BasePromQL *string                    `json:"base_promql"`
	Status     string                     `json:"status"`
}

type Workspace struct {
	Services        []servicecatalog.Service `json:"services"`
	ActiveServiceID string                   `json:"active_service_id"`
	Binding         *ServiceBindingView      `json:"binding,omitempty"`
	Endpoints       []obsendpoint.Endpoint   `json:"endpoints"`
	CollectorGroups []any                    `json:"collector_groups"`
	AlertRules      []any                    `json:"alert_rules"`
	Dashboards      []any                    `json:"dashboards"`
}
