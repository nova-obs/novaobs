package resource

import "time"

type Identity struct {
	ClusterID  string `json:"cluster_id"`
	Namespace  string `json:"namespace"`
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

type ResourceSummary struct {
	Identity  Identity          `json:"identity"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type ResourceDetail struct {
	Identity  Identity          `json:"identity"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
	Spec      map[string]any    `json:"spec"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type ResourceYAML struct {
	Identity Identity `json:"identity"`
	YAML     string   `json:"yaml"`
}

type PodLogResult struct {
	Identity  Identity `json:"identity"`
	Container string   `json:"container"`
	Lines     []string `json:"lines"`
}

type RuntimeGroupsQuery struct {
	ClusterID string
	Namespace string
}

type RuntimeGroupsResponse struct {
	ClusterID string               `json:"cluster_id"`
	Namespace string               `json:"namespace"`
	Groups    []RuntimeGroup       `json:"groups"`
	Summary   RuntimeGroupsSummary `json:"summary"`
}

type RuntimeGroupsSummary struct {
	GroupCount           uint64 `json:"group_count"`
	ServiceCount         uint64 `json:"service_count"`
	WorkloadCount        uint64 `json:"workload_count"`
	PodCount             uint64 `json:"pod_count"`
	PVCCount             uint64 `json:"pvc_count"`
	VirtualServiceCount  uint64 `json:"virtual_service_count"`
	GatewayCount         uint64 `json:"gateway_count"`
	DestinationRuleCount uint64 `json:"destination_rule_count"`
	SecurityPolicyCount  uint64 `json:"security_policy_count"`
}

type RuntimeGroup struct {
	Key         string                `json:"key"`
	DisplayName string                `json:"display_name"`
	IsVirtual   bool                  `json:"is_virtual"`
	Exposures   []RuntimeExposureNode `json:"exposures"`
	Services    []RuntimeServiceNode  `json:"services"`
	Workloads   []RuntimeWorkloadNode `json:"workloads"`
	Summary     RuntimeGroupSummary   `json:"summary"`
}

type RuntimeGroupSummary struct {
	ServicesTotal               uint64 `json:"services_total"`
	WorkloadsTotal              uint64 `json:"workloads_total"`
	PodsTotal                   uint64 `json:"pods_total"`
	RunningPods                 uint64 `json:"running_pods"`
	PendingPods                 uint64 `json:"pending_pods"`
	FailedPods                  uint64 `json:"failed_pods"`
	RestartCount                int32  `json:"restart_count"`
	PersistentVolumeClaimsTotal uint64 `json:"persistent_volume_claims_total"`
	VirtualServicesTotal        uint64 `json:"virtual_services_total"`
	GatewaysTotal               uint64 `json:"gateways_total"`
	DestinationRulesTotal       uint64 `json:"destination_rules_total"`
	SecurityPoliciesTotal       uint64 `json:"security_policies_total"`
}

type RuntimeExposureNode struct {
	Key          string                           `json:"key"`
	Name         string                           `json:"name"`
	Kind         string                           `json:"kind"`
	Hosts        []string                         `json:"hosts"`
	Gateways     []string                         `json:"gateways"`
	ServiceRefs  []string                         `json:"service_refs"`
	RouteTargets []RuntimeRouteTarget             `json:"route_targets"`
	RouteRules   []RuntimeVirtualServiceRouteNode `json:"route_rules"`
	CreatedAt    *string                          `json:"created_at,omitempty"`
}

type RuntimeRouteTarget struct {
	Host   string  `json:"host"`
	Subset *string `json:"subset,omitempty"`
	Port   *string `json:"port,omitempty"`
	Weight *int32  `json:"weight,omitempty"`
}

type RuntimeStringMatchNode struct {
	MatchType string `json:"match_type"`
	Value     string `json:"value"`
}

type RuntimeHeaderMatchNode struct {
	Name    string                 `json:"name"`
	Matcher RuntimeStringMatchNode `json:"matcher"`
}

type RuntimeVirtualServiceMatchNode struct {
	Summary         string                   `json:"summary"`
	URI             *RuntimeStringMatchNode  `json:"uri,omitempty"`
	Scheme          *RuntimeStringMatchNode  `json:"scheme,omitempty"`
	Method          *RuntimeStringMatchNode  `json:"method,omitempty"`
	Authority       *RuntimeStringMatchNode  `json:"authority,omitempty"`
	Headers         []RuntimeHeaderMatchNode `json:"headers"`
	Gateways        []string                 `json:"gateways"`
	SourceLabels    []string                 `json:"source_labels"`
	SourceNamespace *string                  `json:"source_namespace,omitempty"`
	SourceSubnets   []string                 `json:"source_subnets"`
	Port            *string                  `json:"port,omitempty"`
	SNIHosts        []string                 `json:"sni_hosts"`
}

type RuntimeVirtualServiceRouteNode struct {
	Name       *string                          `json:"name,omitempty"`
	Protocol   string                           `json:"protocol"`
	RewriteURI *string                          `json:"rewrite_uri,omitempty"`
	Matches    []RuntimeVirtualServiceMatchNode `json:"matches"`
	Targets    []RuntimeRouteTarget             `json:"targets"`
}

type RuntimeVirtualServiceNode struct {
	Name         string                           `json:"name"`
	Hosts        []string                         `json:"hosts"`
	Gateways     []string                         `json:"gateways"`
	RouteTargets []RuntimeRouteTarget             `json:"route_targets"`
	Routes       []RuntimeVirtualServiceRouteNode `json:"routes"`
	CreatedAt    *string                          `json:"created_at,omitempty"`
}

type RuntimeServicePort struct {
	Name       *string `json:"name,omitempty"`
	Port       int32   `json:"port"`
	TargetPort string  `json:"target_port"`
	Protocol   string  `json:"protocol"`
	NodePort   *int32  `json:"node_port,omitempty"`
}

type RuntimeServiceNode struct {
	Name                   string                       `json:"name"`
	ServiceType            string                       `json:"service_type"`
	ClusterIP              string                       `json:"cluster_ip"`
	Selectors              map[string]string            `json:"selectors"`
	Ports                  []RuntimeServicePort         `json:"ports"`
	CreatedAt              *string                      `json:"created_at,omitempty"`
	Hosts                  []string                     `json:"hosts"`
	VirtualServices        []string                     `json:"virtual_services"`
	VirtualServiceDetails  []RuntimeVirtualServiceNode  `json:"virtual_service_details"`
	Gateways               []string                     `json:"gateways"`
	DestinationRules       []string                     `json:"destination_rules"`
	DestinationRuleDetails []RuntimeDestinationRuleNode `json:"destination_rule_details"`
}

type RuntimeDestinationRuleSubsetNode struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type RuntimeDestinationRuleNode struct {
	Name             string                             `json:"name"`
	Host             string                             `json:"host"`
	Subsets          []string                           `json:"subsets"`
	SubsetDetails    []RuntimeDestinationRuleSubsetNode `json:"subset_details"`
	HasTrafficPolicy bool                               `json:"has_traffic_policy"`
	ExportTo         []string                           `json:"export_to"`
	CreatedAt        *string                            `json:"created_at,omitempty"`
}

type RuntimeSecurityPolicyNode struct {
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Summary   string  `json:"summary"`
	CreatedAt *string `json:"created_at,omitempty"`
}

type RuntimeWorkloadNode struct {
	Key                    string                      `json:"key"`
	Name                   string                      `json:"name"`
	Kind                   string                      `json:"kind"`
	Selector               map[string]string           `json:"selector"`
	TemplateLabels         map[string]string           `json:"template_labels"`
	Replicas               *int32                      `json:"replicas,omitempty"`
	ReadyReplicas          *int32                      `json:"ready_replicas,omitempty"`
	DesiredNumberScheduled *int32                      `json:"desired_number_scheduled,omitempty"`
	CurrentNumberScheduled *int32                      `json:"current_number_scheduled,omitempty"`
	NumberReady            *int32                      `json:"number_ready,omitempty"`
	CreatedAt              *string                     `json:"created_at,omitempty"`
	ServiceAccounts        []string                    `json:"service_accounts"`
	ConfigMaps             []string                    `json:"config_maps"`
	PodsSummary            RuntimePodSummary           `json:"pods_summary"`
	PersistentVolumeClaims []string                    `json:"persistent_volume_claims"`
	HPAs                   []RuntimeHPANode            `json:"hpas"`
	SecurityPolicies       []RuntimeSecurityPolicyNode `json:"security_policies"`
}

type RuntimePodSummary struct {
	Total           uint64 `json:"total"`
	Running         uint64 `json:"running"`
	Pending         uint64 `json:"pending"`
	Failed          uint64 `json:"failed"`
	Succeeded       uint64 `json:"succeeded"`
	ReadyContainers uint64 `json:"ready_containers"`
	TotalContainers uint64 `json:"total_containers"`
	RestartCount    int32  `json:"restart_count"`
}

type RuntimeHPANode struct {
	Name            string `json:"name"`
	MinReplicas     *int32 `json:"min_replicas,omitempty"`
	MaxReplicas     *int32 `json:"max_replicas,omitempty"`
	CurrentReplicas *int32 `json:"current_replicas,omitempty"`
}

type ListFilter struct {
	ClusterID  string
	Namespace  string
	APIVersion string
	Kind       string
	Query      string
	Page       int
	PageSize   int
	Sort       string
	Order      string
}

type DetailQuery struct {
	Identity Identity
}

type PodLogQuery struct {
	ClusterID string
	Namespace string
	Pod       string
	Container string
}
