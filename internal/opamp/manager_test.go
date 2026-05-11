package opamp

import (
	"context"
	"net"
	"testing"

	"novaobs/internal/collectorconfig"

	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/stretchr/testify/require"
)

func TestManagerOffersPendingRemoteConfig(t *testing.T) {
	manager := NewManager()
	manager.QueueDeployment(collectorconfig.RemoteConfigDeployment{
		CollectorInstanceUID: "collector-a",
		Version:              2,
		ConfigHash:           "abc123",
		CollectorYAML:        "receivers:\n  otlp:\n",
		Status:               "pending",
	})

	response := manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig),
	})

	require.NotNil(t, response)
	require.Equal(t, []byte("collector-a"), response.InstanceUid)
	require.NotNil(t, response.RemoteConfig)
	require.Equal(t, []byte("abc123"), response.RemoteConfig.ConfigHash)
	require.Equal(t, []byte("receivers:\n  otlp:\n"), response.RemoteConfig.Config.ConfigMap["collector.yaml"].Body)
	require.Len(t, manager.ListAgents(), 1)
}

func TestManagerOffersPendingRemoteConfigByCollectorGroup(t *testing.T) {
	manager := NewManager()
	manager.RegisterInstanceGroup("collector-a", "7")
	manager.QueueGroupDeployment("7", collectorconfig.RemoteConfigDeployment{
		Version:       2,
		ConfigHash:    "group-hash",
		CollectorYAML: "processors:\n  batch:\n",
		Status:        "pending",
	})

	response := manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig),
	})

	require.NotNil(t, response.RemoteConfig)
	require.Equal(t, []byte("group-hash"), response.RemoteConfig.ConfigHash)
	require.Equal(t, []byte("processors:\n  batch:\n"), response.RemoteConfig.Config.ConfigMap["collector.yaml"].Body)
}

func TestManagerSendsGroupRemoteConfigToConnectedAgent(t *testing.T) {
	manager := NewManager()
	manager.RegisterInstanceGroup("collector-a", "group-001")
	conn := &testConnection{}
	manager.HandleConnectionMessage(context.Background(), conn, &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig),
	})

	sent, err := manager.SendGroupDeployment(context.Background(), "group-001", collectorconfig.RemoteConfigDeployment{
		Version:       2,
		ConfigHash:    "group-hash",
		CollectorYAML: "processors:\n  batch:\n",
		Status:        "pending",
	})

	require.NoError(t, err)
	require.Equal(t, 1, sent)
	require.Len(t, conn.sent, 1)
	require.Equal(t, []byte("collector-a"), conn.sent[0].InstanceUid)
	require.Equal(t, []byte("group-hash"), conn.sent[0].RemoteConfig.ConfigHash)
	require.Equal(t, []byte("processors:\n  batch:\n"), conn.sent[0].RemoteConfig.Config.ConfigMap["collector.yaml"].Body)
}

func TestManagerReportsAgentStateToSink(t *testing.T) {
	manager := NewManager()
	var got AgentState
	manager.SetStateSink(func(_ context.Context, state AgentState) {
		got = state
	})
	manager.RegisterInstanceGroup("collector-a", "group-001")

	manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig),
		Health:       &protobufs.ComponentHealth{Healthy: true},
	})

	require.Equal(t, "collector-a", got.InstanceUID)
	require.Equal(t, "group-001", got.CollectorGroupID)
	require.True(t, got.Online)
	require.True(t, got.Healthy)
	require.True(t, got.RemoteConfigCapable)
}

func TestManagerReportsEffectiveConfigHashToSink(t *testing.T) {
	manager := NewManager()
	var got AgentState
	manager.SetStateSink(func(_ context.Context, state AgentState) {
		got = state
	})

	manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\n")},
		}}},
	})

	require.Equal(t, collectorconfig.HashYAML("receivers:\n  otlp:\n"), got.EffectiveConfigHash)
}

func TestManagerStoresAgentDetailAttributesAndEffectiveConfig(t *testing.T) {
	manager := NewManager()

	manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid:  []byte("collector-a"),
		Capabilities: uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig | protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig),
		AgentDescription: &protobufs.AgentDescription{
			IdentifyingAttributes: []*protobufs.KeyValue{
				{Key: "service.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "otelcol-contrib"}}},
			},
			NonIdentifyingAttributes: []*protobufs.KeyValue{
				{Key: "os.type", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "darwin"}}},
			},
		},
		EffectiveConfig: &protobufs.EffectiveConfig{ConfigMap: &protobufs.AgentConfigMap{ConfigMap: map[string]*protobufs.AgentConfigFile{
			"collector.yaml": {Body: []byte("receivers:\n  otlp:\n")},
		}}},
	})

	detail, ok := manager.GetAgentDetail("collector-a")
	require.True(t, ok)
	require.True(t, detail.State.RemoteConfigCapable)
	require.Equal(t, "service.name", detail.IdentifyingAttributes[0].Key)
	require.Equal(t, "otelcol-contrib", detail.IdentifyingAttributes[0].ValueText)
	require.Equal(t, "os.type", detail.NonIdentifyingAttributes[0].Key)
	require.Equal(t, "darwin", detail.NonIdentifyingAttributes[0].ValueText)
	require.Equal(t, "receivers:\n  otlp:\n", detail.EffectiveConfig)
	require.Equal(t, collectorconfig.HashYAML("receivers:\n  otlp:\n"), detail.State.EffectiveConfigHash)
}

func TestManagerPreservesHealthWhenRemoteConfigStatusOmitsHealth(t *testing.T) {
	manager := NewManager()
	var states []AgentState
	manager.SetStateSink(func(_ context.Context, state AgentState) {
		states = append(states, state)
	})

	manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid: []byte("collector-a"),
		Health:      &protobufs.ComponentHealth{Healthy: true},
	})
	manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid: []byte("collector-a"),
		RemoteConfigStatus: &protobufs.RemoteConfigStatus{
			LastRemoteConfigHash: []byte("hash-001"),
			Status:               protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED,
		},
	})

	require.Len(t, states, 2)
	require.True(t, states[1].Healthy)
	require.False(t, states[1].HealthSet)
	require.Equal(t, "applied", states[1].RemoteConfigStatus)
}

func TestManagerMarksWebSocketConnectionOfflineOnClose(t *testing.T) {
	manager := NewManager()
	var states []AgentState
	manager.SetStateSink(func(_ context.Context, state AgentState) {
		states = append(states, state)
	})
	conn := testConnection{}

	manager.HandleConnectionMessage(context.Background(), &conn, &protobufs.AgentToServer{
		InstanceUid: []byte("collector-a"),
		Health:      &protobufs.ComponentHealth{Healthy: true},
	})
	manager.MarkConnectionClosed(context.Background(), &conn)

	agents := manager.ListAgents()
	require.Len(t, agents, 1)
	require.Equal(t, "collector-a", agents[0].InstanceUID)
	require.False(t, agents[0].Online)
	require.False(t, agents[0].Healthy)
	require.Len(t, states, 2)
	require.True(t, states[0].Online)
	require.False(t, states[1].Online)
}

func TestManagerMarksRemoteConfigApplied(t *testing.T) {
	manager := NewManager()
	manager.QueueDeployment(collectorconfig.RemoteConfigDeployment{
		CollectorInstanceUID: "collector-a",
		Version:              2,
		ConfigHash:           "abc123",
		CollectorYAML:        "receivers:\n  otlp:\n",
		Status:               "pending",
	})

	manager.HandleMessage(context.Background(), &protobufs.AgentToServer{
		InstanceUid: []byte("collector-a"),
		RemoteConfigStatus: &protobufs.RemoteConfigStatus{
			LastRemoteConfigHash: []byte("abc123"),
			Status:               protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED,
		},
	})

	agents := manager.ListAgents()
	require.Len(t, agents, 1)
	require.Equal(t, "applied", agents[0].RemoteConfigStatus)
	require.Equal(t, "abc123", agents[0].LastConfigHash)
	require.Empty(t, manager.PendingConfigHash("collector-a"))
}

type testConnection struct {
	sent []*protobufs.ServerToAgent
}

func (testConnection) Connection() net.Conn {
	return nil
}

func (c *testConnection) Send(_ context.Context, message *protobufs.ServerToAgent) error {
	c.sent = append(c.sent, message)
	return nil
}

func (testConnection) Disconnect() error {
	return nil
}
