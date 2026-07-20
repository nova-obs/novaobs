package terminal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandPolicyParsesReadOnlyKubectlCommand(t *testing.T) {
	parsed, blockReason, err := DefaultCommandPolicy().Parse("kubectl logs deployment/orders-api -n orders --tail=100")

	require.NoError(t, err)
	require.Empty(t, blockReason)
	require.Equal(t, "logs", parsed.Verb)
	require.Equal(t, []string{"deployment/orders-api", "-n", "orders", "--tail=100"}, parsed.Args)
}

func TestCommandPolicyBlocksShellMetaCharacters(t *testing.T) {
	_, blockReason, err := DefaultCommandPolicy().Parse("get pods | cat")

	require.NoError(t, err)
	require.Contains(t, blockReason, "shell")
}

func TestCommandPolicyBlocksHighRiskArgs(t *testing.T) {
	_, blockReason, err := DefaultCommandPolicy().Parse("get pods --token=secret")

	require.NoError(t, err)
	require.Contains(t, blockReason, "--token")
}

func TestCommandPolicyRejectsEmptyCommand(t *testing.T) {
	_, _, err := DefaultCommandPolicy().Parse("kubectl")

	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestCommandPolicyTruncatesOutput(t *testing.T) {
	output, truncated := (CommandPolicy{MaxOutputBytes: 12}).TrimOutput("0123456789abcdef")

	require.True(t, truncated)
	require.Equal(t, "0123456789ab\n... output truncated by NovaAPM terminal policy ...", output)
}

func TestCommandPolicyTruncatesOutputAtUTF8Boundary(t *testing.T) {
	output, truncated := (CommandPolicy{MaxOutputBytes: 7}).TrimOutput("节点状态abcdef")

	require.True(t, truncated)
	require.Equal(t, "节点\n... output truncated by NovaAPM terminal policy ...", output)
}

func TestCommandPolicyKeepsFirstRuneWhenLimitIsSmallerThanRune(t *testing.T) {
	output, truncated := (CommandPolicy{MaxOutputBytes: 1}).TrimOutput("节abc")

	require.True(t, truncated)
	require.Equal(t, "节\n... output truncated by NovaAPM terminal policy ...", output)
}
