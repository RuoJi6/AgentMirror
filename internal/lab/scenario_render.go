package lab

import (
	"html"
	"strings"
	"unicode/utf8"
)

func renderScenarioResult(rule, response, context Doc, method string, ruleMatched bool, trace []any) Doc {
	body := str(response["body"])
	hasPrompt := strings.Contains(body, "{{prompt}}")
	profile := object(rule["profile"])
	instruction := ""
	if hasPrompt {
		if profile != nil {
			instruction = scenarioInstruction(profile, context)
		} else {
			instruction = str(context["instruction"])
		}
		if strings.TrimSpace(instruction) == "" {
			fail(500, "场景响应缺少已绑定的提示词")
		}
	}
	format, prefix := str(response["format"]), str(response["prompt_prefix"])
	slots := strings.Count(body, "{{prompt}}")
	expectedSize := int64(len(body)-slots*len("{{prompt}}")) + int64(slots)*scenarioPromptSize(instruction, format, prefix)
	if slots > 32 || expectedSize > scenarioOutputLimit {
		fail(413, "场景渲染结果不得超过 2 MiB")
	}
	switch str(response["format"]) {
	case "json":
		if body != "" {
			value, ok := scenarioJSON(body)
			if !ok {
				fail(500, "已发布场景的 JSON 响应无效")
			}
			body = dump(replaceScenarioJSON(value, instruction))
		}
	case "html":
		body = strings.ReplaceAll(body, "{{prompt}}", html.EscapeString(instruction))
	default:
		body = strings.ReplaceAll(body, "{{prompt}}", prefixScenarioPrompt(instruction, prefix))
	}
	if len(body) > scenarioOutputLimit {
		fail(413, "场景渲染结果不得超过 2 MiB")
	}
	deliveries := []any{}
	if hasPrompt && ruleMatched && method != "HEAD" {
		deliveries = append(deliveries, Doc{"binding_id": rule["binding_id"], "rule_id": rule["id"], "profile_id": profile["id"], "profile_name": profile["name"], "profile_version": profile["version"], "slot": "prompt", "format": response["format"]})
	}
	headers := Doc{}
	for key, value := range object(response["headers"]) {
		headers[key] = value
	}
	return Doc{"matched": true, "rule_matched": ruleMatched, "fallback": !ruleMatched, "binding_id": rule["binding_id"], "rule_id": rule["id"], "status": response["status"], "content_type": response["content_type"], "headers": headers, "body": body, "body_bytes": len(body), "contains_prompt": hasPrompt, "deliveries": deliveries, "trace": trace}
}

// Match renderInstruction's one-pass semantics while bounding expansion of
// fields/commands before allocating the result, then bound the response itself.
func scenarioInstruction(profile, context Doc) string {
	values := Doc{"run_id": context["run_id"], "token": context["token"], "callback_url": context["callback_url"], "fields": pretty(profile["fields"]), "commands": pretty(profile["commands"])}
	body := str(profile["body"])
	size := int64(len(body))
	for _, match := range variablePattern.FindAllStringSubmatch(body, -1) {
		key := strings.TrimSpace(match[1])
		if validVariable(key) {
			size += int64(len(str(values[key])) - len(match[0]))
		}
		if size > scenarioOutputLimit+int64(len(body)) {
			fail(413, "场景提示词展开结果不得超过 2 MiB")
		}
	}
	if size > scenarioOutputLimit {
		fail(413, "场景提示词展开结果不得超过 2 MiB")
	}
	return variablePattern.ReplaceAllStringFunc(body, func(token string) string {
		match := variablePattern.FindStringSubmatch(token)
		key := strings.TrimSpace(match[1])
		if validVariable(key) {
			return str(values[key])
		}
		return token
	})
}

func scenarioPromptSize(prompt, format, prefix string) int64 {
	if format == "text" || format == "" {
		lines := int64(1)
		previousCR := false
		for _, r := range prompt {
			if r == '\r' || r == '\u0085' || r == '\u2028' || r == '\u2029' || (r == '\n' && !previousCR) {
				lines++
			}
			previousCR = r == '\r'
		}
		return int64(len(prompt)) + lines*int64(len(prefix))
	}
	size := int64(0)
	for _, r := range prompt {
		if format == "html" {
			switch r {
			case '&', '\'', '"':
				size += 5
			case '<', '>':
				size += 4
			default:
				size += int64(utf8.RuneLen(r))
			}
		} else {
			switch r {
			case '\\', '"', '\n', '\r', '\t', '\b', '\f':
				size += 2
			case '\u2028', '\u2029':
				size += 6
			default:
				if r < 0x20 {
					size += 6
				} else {
					size += int64(utf8.RuneLen(r))
				}
			}
		}
	}
	return size
}

func prefixScenarioPrompt(prompt, prefix string) string {
	if prefix == "" {
		return prompt
	}
	var result strings.Builder
	result.Grow(int(scenarioPromptSize(prompt, "text", prefix)))
	result.WriteString(prefix)
	for i := 0; i < len(prompt); {
		r, size := utf8.DecodeRuneInString(prompt[i:])
		result.WriteString(prompt[i : i+size])
		i += size
		if r == '\r' && i < len(prompt) && prompt[i] == '\n' {
			result.WriteByte('\n')
			i++
		}
		if r == '\r' || r == '\n' || r == '\u0085' || r == '\u2028' || r == '\u2029' {
			result.WriteString(prefix)
		}
	}
	return result.String()
}

func replaceScenarioJSON(value any, prompt string) any {
	switch v := value.(type) {
	case string:
		return strings.ReplaceAll(v, "{{prompt}}", prompt)
	case map[string]any:
		result := map[string]any{}
		for key, child := range v {
			result[key] = replaceScenarioJSON(child, prompt)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, child := range v {
			result[i] = replaceScenarioJSON(child, prompt)
		}
		return result
	default:
		return value
	}
}
