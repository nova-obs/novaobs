package opamp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"novaapm/internal/collectorconfig"

	opampproto "github.com/open-telemetry/opamp-go/protobufs"
	opampserver "github.com/open-telemetry/opamp-go/server"
	opampservertypes "github.com/open-telemetry/opamp-go/server/types"
)

type AgentState struct {
	InstanceUID         string    `json:"instance_uid"`
	OpAMPInstanceUID    string    `json:"opamp_instance_uid"`
	RuntimeIdentity     string    `json:"runtime_identity"`
	CollectorGroupID    string    `json:"collector_group_id,omitempty"`
	ServiceID           string    `json:"service_id"`
	ClusterID           string    `json:"cluster_id"`
	Namespace           string    `json:"namespace"`
	AgentNamespace      string    `json:"agent_namespace"`
	Hostname            string    `json:"hostname"`
	PodUID              string    `json:"pod_uid"`
	PodName             string    `json:"pod_name"`
	NodeName            string    `json:"node_name"`
	PodIP               string    `json:"pod_ip"`
	Version             string    `json:"version"`
	Online              bool      `json:"online"`
	Healthy             bool      `json:"healthy"`
	HealthSet           bool      `json:"-"`
	Capabilities        uint64    `json:"capabilities"`
	RemoteConfigCapable bool      `json:"remote_config_capable"`
	EffectiveConfigHash string    `json:"effective_config_hash"`
	RemoteConfigStatus  string    `json:"remote_config_status"`
	LastConfigHash      string    `json:"last_config_hash"`
	LastError           string    `json:"last_error"`
	LastSeenAt          time.Time `json:"last_seen_at"`
}

type AgentAttribute struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	ValueText   string `json:"value_text"`
	Identifying bool   `json:"identifying"`
}

type AgentRuntimeDetail struct {
	State                    AgentState        `json:"state"`
	IdentifyingAttributes    []AgentAttribute  `json:"identifying_attributes"`
	NonIdentifyingAttributes []AgentAttribute  `json:"non_identifying_attributes"`
	EffectiveConfig          string            `json:"effective_config"`
	EffectiveConfigFiles     map[string]string `json:"effective_config_files"`
	LastRemoteConfig         string            `json:"last_remote_config"`
	LastRemoteConfigHash     string            `json:"last_remote_config_hash"`
	LastRemoteConfigFiles    map[string]string `json:"last_remote_config_files"`
	LastSeenAt               time.Time         `json:"last_seen_at"`
}

type StateSink func(context.Context, AgentState)

type Manager struct {
	mu             sync.RWMutex
	sendMu         sync.Mutex
	agents         map[string]AgentState
	details        map[string]AgentRuntimeDetail
	pending        map[string]collectorconfig.RemoteConfigDeployment
	instanceGroups map[string]string
	serviceAgents  map[string]string
	connectionUIDs map[opampservertypes.Connection]string
	uidConnections map[string]opampservertypes.Connection
	groupPending   map[string]collectorconfig.RemoteConfigDeployment
	stateSinks     []StateSink
	handler        opampserver.HTTPHandlerFunc
}

func NewManager() *Manager {
	manager := &Manager{
		agents:         map[string]AgentState{},
		details:        map[string]AgentRuntimeDetail{},
		pending:        map[string]collectorconfig.RemoteConfigDeployment{},
		instanceGroups: map[string]string{},
		serviceAgents:  map[string]string{},
		connectionUIDs: map[opampservertypes.Connection]string{},
		uidConnections: map[string]opampservertypes.Connection{},
		groupPending:   map[string]collectorconfig.RemoteConfigDeployment{},
	}
	server := opampserver.New(nil)
	handler, _, err := server.Attach(opampserver.Settings{
		EnableCompression: true,
		Callbacks: opampservertypes.Callbacks{
			OnConnecting: func(request *http.Request) opampservertypes.ConnectionResponse {
				isWebSocket := strings.EqualFold(request.Header.Get("Upgrade"), "websocket")
				return opampservertypes.ConnectionResponse{
					Accept: true,
					ConnectionCallbacks: opampservertypes.ConnectionCallbacks{
						OnMessage: func(ctx context.Context, conn opampservertypes.Connection, message *opampproto.AgentToServer) *opampproto.ServerToAgent {
							return manager.HandleConnectionMessage(ctx, conn, message)
						},
						OnConnectionClose: func(conn opampservertypes.Connection) {
							if isWebSocket {
								manager.MarkConnectionClosed(context.Background(), conn)
							}
						},
					},
				}
			},
		},
	})
	if err != nil {
		panic(err)
	}
	manager.handler = handler
	return manager
}

func (m *Manager) SetStateSink(sink StateSink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sink == nil {
		m.stateSinks = nil
		return
	}
	m.stateSinks = []StateSink{sink}
}

func (m *Manager) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	m.handler(writer, request)
}

func (m *Manager) QueueDeployment(deployment collectorconfig.RemoteConfigDeployment) {
	uid := deployment.CollectorInstanceUID
	if uid == "" {
		uid = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[uid] = deployment
}

func (m *Manager) PendingConfigHash(instanceUID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pending[instanceUID].ConfigHash
}

func (m *Manager) RegisterInstanceGroup(instanceUID string, groupID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instanceGroups[instanceUID] = groupID
	state := m.agents[instanceUID]
	state.InstanceUID = instanceUID
	state.CollectorGroupID = groupID
	delete(m.serviceAgents, instanceUID)
	state.ServiceID = ""
	m.agents[instanceUID] = state
}

func (m *Manager) UnregisterInstanceGroup(instanceUID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.instanceGroups, instanceUID)
	state := m.agents[instanceUID]
	state.InstanceUID = instanceUID
	state.CollectorGroupID = ""
	m.agents[instanceUID] = state
}

func (m *Manager) QueueGroupDeployment(groupID string, deployment collectorconfig.RemoteConfigDeployment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.groupPending[groupID] = deployment
}

func (m *Manager) SendGroupDeployment(ctx context.Context, groupID string, deployment collectorconfig.RemoteConfigDeployment) (int, error) {
	type target struct {
		uid  string
		conn opampservertypes.Connection
	}
	var targets []target
	m.mu.Lock()
	m.groupPending[groupID] = deployment
	for uid, state := range m.agents {
		if state.CollectorGroupID != groupID || !state.Online || !state.RemoteConfigCapable {
			continue
		}
		conn := m.uidConnections[uid]
		if conn == nil {
			continue
		}
		targets = append(targets, target{uid: uid, conn: conn})
	}
	m.mu.Unlock()

	for _, item := range targets {
		m.sendMu.Lock()
		err := item.conn.Send(ctx, remoteConfigMessage(item.uid, deployment))
		m.sendMu.Unlock()
		if err != nil {
			return len(targets), err
		}
		m.recordLastRemoteConfig(item.uid, deployment)
	}
	return len(targets), nil
}

func (m *Manager) PendingGroupConfigHash(groupID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.groupPending[groupID].ConfigHash
}

func (m *Manager) HandleMessage(ctx context.Context, message *opampproto.AgentToServer) *opampproto.ServerToAgent {
	return m.HandleConnectionMessage(ctx, nil, message)
}

func (m *Manager) HandleConnectionMessage(ctx context.Context, conn opampservertypes.Connection, message *opampproto.AgentToServer) *opampproto.ServerToAgent {
	uid := instanceUIDToText(message.InstanceUid)
	now := time.Now().UTC()
	m.mu.Lock()
	if conn != nil {
		m.connectionUIDs[conn] = uid
		m.uidConnections[uid] = conn
	}
	groupID := m.instanceGroups[uid]
	state := m.agents[uid]
	state.InstanceUID = uid
	state.OpAMPInstanceUID = uid
	state.CollectorGroupID = groupID
	state.ServiceID = m.serviceAgents[uid]
	state.Online = true
	state.Capabilities = message.Capabilities
	state.RemoteConfigCapable = agentAcceptsRemoteConfig(message.Capabilities)
	state.LastSeenAt = now
	detail := m.details[uid]
	detail.State = state
	detail.LastSeenAt = now
	if message.Health != nil {
		state.Healthy = message.Health.Healthy
		state.HealthSet = true
		state.LastError = message.Health.LastError
	} else {
		state.HealthSet = false
	}
	if message.AgentDescription != nil {
		detail.IdentifyingAttributes = keyValuesToAttributes(message.AgentDescription.IdentifyingAttributes, true)
		detail.NonIdentifyingAttributes = keyValuesToAttributes(message.AgentDescription.NonIdentifyingAttributes, false)
		applyAgentDescriptionState(&state, detail.IdentifyingAttributes, detail.NonIdentifyingAttributes)
		if state.CollectorGroupID != "" {
			m.instanceGroups[uid] = state.CollectorGroupID
			groupID = state.CollectorGroupID
		}
	}
	if message.EffectiveConfig != nil {
		state.EffectiveConfigHash = effectiveConfigHash(message.EffectiveConfig)
		detail.EffectiveConfigFiles = configMapFiles(message.EffectiveConfig.ConfigMap)
		detail.EffectiveConfig = joinConfigFiles(detail.EffectiveConfigFiles)
	}
	if message.RemoteConfigStatus != nil {
		state.RemoteConfigStatus = remoteConfigStatusText(message.RemoteConfigStatus.Status)
		state.LastConfigHash = hashToText(message.RemoteConfigStatus.LastRemoteConfigHash)
		state.LastError = message.RemoteConfigStatus.ErrorMessage
		if state.RemoteConfigStatus == "applied" {
			delete(m.pending, uid)
			if groupID != "" && state.LastConfigHash == m.groupPending[groupID].ConfigHash {
				delete(m.groupPending, groupID)
			}
		}
	}
	m.agents[uid] = state
	detail.State = state
	m.details[uid] = detail
	pending, hasPending := m.pending[uid]
	if groupID != "" {
		if groupDeployment, ok := m.groupPending[groupID]; ok {
			pending = groupDeployment
			hasPending = true
		}
	}
	sinks := append([]StateSink{}, m.stateSinks...)
	m.mu.Unlock()

	for _, sink := range sinks {
		sink(ctx, state)
	}

	response := &opampproto.ServerToAgent{
		InstanceUid:  message.InstanceUid,
		Capabilities: serverCapabilities(),
	}
	if hasPending && agentAcceptsRemoteConfig(message.Capabilities) {
		response.RemoteConfig = remoteConfigPayload(pending)
		m.recordLastRemoteConfig(uid, pending)
	}
	return response
}

func (m *Manager) MarkConnectionClosed(ctx context.Context, conn opampservertypes.Connection) {
	m.mu.Lock()
	uid := m.connectionUIDs[conn]
	delete(m.connectionUIDs, conn)
	if uid == "" {
		m.mu.Unlock()
		return
	}
	state := m.agents[uid]
	state.InstanceUID = uid
	state.Online = false
	state.Healthy = false
	state.HealthSet = true
	state.LastSeenAt = time.Now().UTC()
	m.agents[uid] = state
	detail := m.details[uid]
	detail.State = state
	detail.LastSeenAt = state.LastSeenAt
	m.details[uid] = detail
	delete(m.uidConnections, uid)
	sinks := append([]StateSink{}, m.stateSinks...)
	m.mu.Unlock()

	for _, sink := range sinks {
		sink(ctx, state)
	}
}

func (m *Manager) ListAgents() []AgentState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	agents := make([]AgentState, 0, len(m.agents))
	for _, agent := range m.agents {
		agents = append(agents, agent)
	}
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].InstanceUID < agents[j].InstanceUID
	})
	return agents
}

func (m *Manager) GetAgentDetail(instanceUID string) (AgentRuntimeDetail, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	detail, ok := m.details[instanceUID]
	if !ok {
		state, stateOK := m.agents[instanceUID]
		if !stateOK {
			return AgentRuntimeDetail{}, false
		}
		detail = AgentRuntimeDetail{State: state, LastSeenAt: state.LastSeenAt}
	}
	detail.State = m.agents[instanceUID]
	detail.IdentifyingAttributes = append([]AgentAttribute{}, detail.IdentifyingAttributes...)
	detail.NonIdentifyingAttributes = append([]AgentAttribute{}, detail.NonIdentifyingAttributes...)
	detail.EffectiveConfigFiles = copyStringMap(detail.EffectiveConfigFiles)
	detail.LastRemoteConfigFiles = copyStringMap(detail.LastRemoteConfigFiles)
	return detail, true
}

func (m *Manager) recordLastRemoteConfig(instanceUID string, deployment collectorconfig.RemoteConfigDeployment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	detail := m.details[instanceUID]
	detail.State = m.agents[instanceUID]
	detail.LastRemoteConfig = deployment.CollectorYAML
	detail.LastRemoteConfigHash = deployment.ConfigHash
	detail.LastRemoteConfigFiles = map[string]string{"collector.yaml": deployment.CollectorYAML}
	detail.LastSeenAt = detail.State.LastSeenAt
	m.details[instanceUID] = detail
}

func agentAcceptsRemoteConfig(capabilities uint64) bool {
	return capabilities&uint64(opampproto.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig) != 0
}

func serverCapabilities() uint64 {
	return uint64(opampproto.ServerCapabilities_ServerCapabilities_AcceptsStatus) |
		uint64(opampproto.ServerCapabilities_ServerCapabilities_OffersRemoteConfig) |
		uint64(opampproto.ServerCapabilities_ServerCapabilities_AcceptsEffectiveConfig)
}

func remoteConfigMessage(instanceUID string, deployment collectorconfig.RemoteConfigDeployment) *opampproto.ServerToAgent {
	return &opampproto.ServerToAgent{
		InstanceUid:  []byte(instanceUID),
		Capabilities: serverCapabilities(),
		RemoteConfig: remoteConfigPayload(deployment),
	}
}

func remoteConfigPayload(deployment collectorconfig.RemoteConfigDeployment) *opampproto.AgentRemoteConfig {
	files := deployment.ConfigFiles
	if len(files) == 0 {
		files = map[string]string{"collector.yaml": deployment.CollectorYAML}
	}
	configMap := map[string]*opampproto.AgentConfigFile{}
	for name, body := range files {
		configMap[name] = &opampproto.AgentConfigFile{Body: []byte(body)}
	}
	return &opampproto.AgentRemoteConfig{
		ConfigHash: []byte(deployment.ConfigHash),
		Config: &opampproto.AgentConfigMap{
			ConfigMap: configMap,
		},
	}
}

func HashConfigFiles(files map[string]string) string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte(files[key]))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func effectiveConfigHash(config *opampproto.EffectiveConfig) string {
	if config == nil || config.ConfigMap == nil || len(config.ConfigMap.ConfigMap) == 0 {
		return ""
	}
	if file := config.ConfigMap.ConfigMap["collector.yaml"]; file != nil {
		return collectorconfig.HashYAML(string(file.Body))
	}
	keys := make([]string, 0, len(config.ConfigMap.ConfigMap))
	for key := range config.ConfigMap.ConfigMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		file := config.ConfigMap.ConfigMap[key]
		if file == nil {
			continue
		}
		builder.WriteString(key)
		builder.WriteByte('\n')
		builder.Write(file.Body)
		builder.WriteByte('\n')
	}
	return collectorconfig.HashYAML(builder.String())
}

func keyValuesToAttributes(values []*opampproto.KeyValue, identifying bool) []AgentAttribute {
	attrs := make([]AgentAttribute, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		converted := anyValueToGo(value.Value)
		attrs = append(attrs, AgentAttribute{
			Key:         value.Key,
			Value:       converted,
			ValueText:   anyValueText(converted),
			Identifying: identifying,
		})
	}
	return attrs
}

func applyAgentDescriptionState(state *AgentState, groups ...[]AgentAttribute) {
	attrs := map[string]string{}
	for _, group := range groups {
		for _, attr := range group {
			if strings.TrimSpace(attr.ValueText) == "" {
				continue
			}
			attrs[attr.Key] = attr.ValueText
		}
	}
	applyAttr := func(target *string, keys ...string) {
		if value := firstAttr(attrs, keys...); value != "" {
			*target = value
		}
	}
	applyAttr(&state.ClusterID, "novaapm.cluster.id", "k8s.cluster.name", "k8s.cluster.id")
	applyAttr(&state.CollectorGroupID, "novaapm.collector.group_id", "collector_group_id")
	applyAttr(&state.Namespace, "k8s.namespace.name", "novaapm.agent.namespace")
	applyAttr(&state.AgentNamespace, "novaapm.agent.namespace", "k8s.namespace.name")
	applyAttr(&state.Hostname, "host.name")
	applyAttr(&state.PodUID, "k8s.pod.uid")
	applyAttr(&state.PodName, "k8s.pod.name")
	applyAttr(&state.NodeName, "k8s.node.name")
	applyAttr(&state.PodIP, "k8s.pod.ip", "net.host.ip")
	applyAttr(&state.Version, "service.version")
	if state.RuntimeIdentity == "" && state.ClusterID != "" && state.CollectorGroupID != "" && state.NodeName != "" {
		state.RuntimeIdentity = "k8s:" + state.ClusterID + ":" + state.CollectorGroupID + ":" + state.NodeName
	}
}

func firstAttr(attrs map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(attrs[key]); value != "" {
			return value
		}
	}
	return ""
}

func anyValueToGo(value *opampproto.AnyValue) any {
	if value == nil {
		return nil
	}
	switch typed := value.GetValue().(type) {
	case *opampproto.AnyValue_StringValue:
		return typed.StringValue
	case *opampproto.AnyValue_BoolValue:
		return typed.BoolValue
	case *opampproto.AnyValue_IntValue:
		return typed.IntValue
	case *opampproto.AnyValue_DoubleValue:
		return typed.DoubleValue
	case *opampproto.AnyValue_BytesValue:
		return hex.EncodeToString(typed.BytesValue)
	case *opampproto.AnyValue_ArrayValue:
		items := make([]any, 0, len(typed.ArrayValue.GetValues()))
		for _, item := range typed.ArrayValue.GetValues() {
			items = append(items, anyValueToGo(item))
		}
		return items
	case *opampproto.AnyValue_KvlistValue:
		out := map[string]any{}
		for _, item := range typed.KvlistValue.GetValues() {
			if item == nil {
				continue
			}
			out[item.Key] = anyValueToGo(item.Value)
		}
		return out
	default:
		return nil
	}
}

func anyValueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func configMapFiles(config *opampproto.AgentConfigMap) map[string]string {
	if config == nil || len(config.ConfigMap) == 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	for name, file := range config.ConfigMap {
		if file == nil {
			continue
		}
		out[name] = string(file.Body)
	}
	return out
}

func joinConfigFiles(files map[string]string) string {
	if len(files) == 0 {
		return ""
	}
	if body, ok := files["collector.yaml"]; ok {
		return body
	}
	if body, ok := files[""]; ok {
		return body
	}
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for index, key := range keys {
		if index > 0 {
			builder.WriteString("\n---\n")
		}
		builder.WriteString(files[key])
	}
	return builder.String()
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func remoteConfigStatusText(status opampproto.RemoteConfigStatuses) string {
	switch status {
	case opampproto.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED:
		return "applied"
	case opampproto.RemoteConfigStatuses_RemoteConfigStatuses_APPLYING:
		return "applying"
	case opampproto.RemoteConfigStatuses_RemoteConfigStatuses_FAILED:
		return "failed"
	default:
		return "unset"
	}
}

func hashToText(hash []byte) string {
	if len(hash) == 0 {
		return ""
	}
	if looksLikeHexText(hash) {
		return string(hash)
	}
	return hex.EncodeToString(hash)
}

func looksLikeHexText(value []byte) bool {
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func instanceUIDToText(value []byte) string {
	for _, char := range value {
		if char < 32 || char > 126 {
			return hex.EncodeToString(value)
		}
	}
	return string(value)
}

func (m *Manager) RegisterInstanceService(instanceUID string, serviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.serviceAgents[instanceUID] = serviceID
	delete(m.instanceGroups, instanceUID)
	state := m.agents[instanceUID]
	state.InstanceUID = instanceUID
	state.ServiceID = serviceID
	state.CollectorGroupID = ""
	m.agents[instanceUID] = state
}

func (m *Manager) UnregisterInstanceService(instanceUID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.serviceAgents, instanceUID)
	state := m.agents[instanceUID]
	state.InstanceUID = instanceUID
	state.ServiceID = ""
	m.agents[instanceUID] = state
}

func (m *Manager) SendServiceDeployment(ctx context.Context, serviceID string, deployment collectorconfig.RemoteConfigDeployment) (int, error) {
	type target struct {
		uid  string
		conn opampservertypes.Connection
	}
	var targets []target
	m.mu.Lock()
	for uid, state := range m.agents {
		if state.ServiceID != serviceID || !state.Online || !state.RemoteConfigCapable {
			continue
		}
		conn := m.uidConnections[uid]
		if conn == nil {
			continue
		}
		targets = append(targets, target{uid: uid, conn: conn})
	}
	m.mu.Unlock()

	for _, item := range targets {
		m.sendMu.Lock()
		err := item.conn.Send(ctx, remoteConfigMessage(item.uid, deployment))
		m.sendMu.Unlock()
		if err != nil {
			return len(targets), err
		}
		m.recordLastRemoteConfig(item.uid, deployment)
	}
	return len(targets), nil
}
