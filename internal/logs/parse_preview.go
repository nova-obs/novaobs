package logs

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

func (s Service) PreviewParseRules(ctx context.Context, req ParsePreviewRequest) (ParsePreviewResult, error) {
	_ = ctx
	fields := map[string]any{"body": req.Sample}
	warnings := []string{}
	errors := []string{}
	rules := normalizeParseRules(req.ParseRules)
	if len(rules) == 0 {
		return ParsePreviewResult{Status: "ok", Fields: fields, Warnings: []string{"未配置解析规则，日志将按原文写入"}, Errors: errors}, nil
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		switch rule.RuleType {
		case ParseRuleJSON:
			mergeJSONPreviewFields(fields, req.Sample, rule.Name, &errors)
		case ParseRuleRegex:
			mergeRegexPreviewFields(fields, req.Sample, rule, &errors)
		default:
			errors = append(errors, fmt.Sprintf("%s: 日志解析规则只支持 regex 或 json", rule.Name))
		}
	}
	status := "ok"
	if len(errors) > 0 {
		status = "error"
	}
	return ParsePreviewResult{Status: status, Fields: fields, Warnings: warnings, Errors: errors}, nil
}

func mergeJSONPreviewFields(fields map[string]any, sample string, name string, errors *[]string) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(sample), &parsed); err != nil {
		*errors = append(*errors, fmt.Sprintf("%s: JSON 解析失败: %v", name, err))
		return
	}
	for key, value := range parsed {
		fields[key] = value
	}
}

func mergeRegexPreviewFields(fields map[string]any, sample string, rule LogParseRule, errors *[]string) {
	if !strings.Contains(rule.Pattern, "?P<") {
		*errors = append(*errors, fmt.Sprintf("%s: regex 解析规则必须使用命名捕获组", rule.Name))
		return
	}
	compiled, err := regexp.Compile(rule.Pattern)
	if err != nil {
		*errors = append(*errors, fmt.Sprintf("%s: Regex 编译失败: %v", rule.Name, err))
		return
	}
	matches := compiled.FindStringSubmatch(sample)
	if matches == nil {
		*errors = append(*errors, fmt.Sprintf("%s: 样本未匹配当前 Regex", rule.Name))
		return
	}
	for index, name := range compiled.SubexpNames() {
		if index == 0 || strings.TrimSpace(name) == "" {
			continue
		}
		fields[name] = matches[index]
	}
}
