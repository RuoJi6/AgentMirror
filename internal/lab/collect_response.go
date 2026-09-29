package lab

import (
	"html"
	"strconv"
	"strings"
	"unicode/utf8"
)

func defaultCollectResponse() Doc {
	return Doc{"enabled": false, "status": 200, "format": "json", "content_type": "application/json; charset=utf-8", "headers": Doc{}, "body": `{"ok":true,"message":"回传已接收","receipt_id":"{{receipt_id}}"}`}
}

func defaultCollectRejectedResponse() Doc {
	return Doc{"enabled": false, "status": 403, "format": "json", "content_type": "application/json; charset=utf-8", "headers": Doc{}, "body": `{"error":"实验编号或令牌无效"}`}
}

func collectRejectedResponseConfig(config Doc) Doc {
	if custom := object(config["collect_rejected_response"]); custom != nil {
		return clone(custom)
	}
	return defaultCollectRejectedResponse()
}

func collectResponseConfig(config Doc) Doc {
	if custom := object(config["collect_response"]); custom != nil {
		return clone(custom)
	}
	return defaultCollectResponse()
}

func validateCollectResponse(raw Doc) Doc {
	if raw == nil {
		fail(400, "回传响应配置必须是对象")
	}
	enabled, ok := optional(raw, "enabled", false).(bool)
	if !ok {
		fail(400, "自定义响应开关必须是布尔值")
	}
	body, ok := raw["body"].(string)
	if !ok || !utf8.ValidString(body) || len(body) > scenarioBodyLimit {
		fail(400, "回传响应正文必须是 UTF-8 文本且不超过 128 KiB")
	}
	// Reuse status, MIME and header checks, without the scenario-specific prompt
	// slot rules. Receipt templates only substitute the two documented IDs.
	metadata := clone(raw)
	metadata["body"] = ""
	delete(metadata, "prompt_prefix")
	result := validateScenarioResponse(metadata, false)
	delete(result, "prompt_prefix")
	status := integer(result["status"])
	if (status == 204 || status == 205 || status == 304) && body != "" {
		fail(400, "该状态码不能包含响应正文")
	}
	if result["format"] == "json" && body != "" {
		if _, valid := scenarioJSON(body); !valid {
			fail(400, "回传响应正文必须是有效 JSON；变量请放在字符串值中")
		}
	}
	result["enabled"], result["body"] = enabled, body
	return result
}

func (s *Store) saveCollectResponse(raw Doc) Doc {
	result := validateCollectResponse(raw)
	s.write(func(q queryer) {
		config := settings(q)
		config["collect_response"] = result
		putSettings(q, config)
	})
	return result
}

func renderCollectRejectedResponse(config Doc, runID string) Doc {
	if !boolean(config["enabled"]) {
		config = defaultCollectRejectedResponse()
		config["enabled"] = true
	}
	return renderCollectResponse(config, runID, Doc{})
}

func renderCollectResponse(config Doc, runID string, receipt Doc) Doc {
	if !boolean(config["enabled"]) {
		return Doc{"status": 201, "content_type": "application/json; charset=utf-8", "headers": Doc{}, "body": dump(receipt)}
	}
	variables := []string{"{{run_id}}", runID}
	if receipt["receipt_id"] != nil {
		variables = append(variables, "{{receipt_id}}", strconv.Itoa(integer(receipt["receipt_id"])))
	}
	ids := strings.NewReplacer(variables...)
	body := str(config["body"])
	switch config["format"] {
	case "json":
		if body != "" {
			value, valid := scenarioJSON(body)
			if !valid {
				fail(500, "已保存的回传响应配置无效")
			}
			var replace func(any) any
			replace = func(value any) any {
				switch v := value.(type) {
				case string:
					return ids.Replace(v)
				case map[string]any:
					for key, child := range v {
						v[key] = replace(child)
					}
				case []any:
					for i, child := range v {
						v[i] = replace(child)
					}
				}
				return value
			}
			body = dump(replace(value))
		}
	case "html":
		variables[1] = html.EscapeString(runID)
		body = strings.NewReplacer(variables...).Replace(body)
	default:
		body = ids.Replace(body)
	}
	return Doc{"status": config["status"], "content_type": config["content_type"], "headers": config["headers"], "body": body}
}
