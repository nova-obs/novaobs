package terminal

import (
	"fmt"
	"strings"
	"unicode"
)

const defaultMaxOutputBytes = 16 * 1024

type CommandPolicy struct {
	MaxOutputBytes int
}

type ParsedCommand struct {
	Verb string
	Args []string
}

func DefaultCommandPolicy() CommandPolicy {
	return CommandPolicy{MaxOutputBytes: defaultMaxOutputBytes}
}

func (p CommandPolicy) Parse(command string) (ParsedCommand, string, error) {
	if containsShellMeta(command) {
		return ParsedCommand{}, "命令包含 shell 元字符，NovaAPM 终端第一版只接受结构化 kubectl 参数", nil
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ParsedCommand{}, "", ErrInvalidRequest
	}
	if fields[0] == "kubectl" {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return ParsedCommand{}, "", ErrInvalidRequest
	}
	verb := strings.ToLower(fields[0])
	if !allowedReadOnlyVerb(verb) {
		return ParsedCommand{}, fmt.Sprintf("动词 %q 不在只读允许列表中", verb), nil
	}
	args := append([]string{}, fields[1:]...)
	for _, arg := range args {
		if blockedArg(arg) {
			return ParsedCommand{}, fmt.Sprintf("参数 %q 属于高风险终端能力，已阻断", arg), nil
		}
	}
	return ParsedCommand{Verb: verb, Args: args}, "", nil
}

func (p CommandPolicy) TrimOutput(output string) (string, bool) {
	limit := p.MaxOutputBytes
	if limit <= 0 {
		limit = defaultMaxOutputBytes
	}
	if len([]byte(output)) <= limit {
		return output, false
	}
	cut := 0
	for index := range output {
		if index > limit {
			break
		}
		cut = index
	}
	if cut == 0 {
		for index := range output {
			if index > 0 {
				cut = index
				break
			}
		}
		if cut == 0 {
			cut = len(output)
		}
	}
	return output[:cut] + "\n... output truncated by NovaAPM terminal policy ...", true
}

func allowedReadOnlyVerb(verb string) bool {
	switch verb {
	case "get", "describe", "logs", "top", "explain", "api-resources", "api-versions":
		return true
	default:
		return false
	}
}

func blockedArg(arg string) bool {
	lower := strings.ToLower(strings.TrimSpace(arg))
	switch lower {
	case "exec", "cp", "attach", "port-forward", "proxy", "delete", "apply", "replace", "patch", "scale", "rollout", "cordon", "uncordon", "drain", "taint", "label", "annotate", "create":
		return true
	default:
		return strings.HasPrefix(lower, "--kubeconfig") || strings.HasPrefix(lower, "--token") || strings.HasPrefix(lower, "--as=")
	}
}

func containsShellMeta(command string) bool {
	for _, value := range command {
		switch value {
		case '|', '&', ';', '`', '$', '>', '<', '\n', '\r':
			return true
		}
		if unicode.IsControl(value) && value != '\t' {
			return true
		}
	}
	return false
}
