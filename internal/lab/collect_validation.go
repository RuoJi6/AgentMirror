package lab

import (
	"net"
	"strings"
	"time"
	"unicode/utf8"
)

func defaultCollectValidation() Doc {
	return Doc{"enabled": false, "fields": []any{}}
}

// Collection validation describes the submitted JSON shape. It cannot establish
// where a value came from or whether a client executed a command.
func validateCollectValidation(raw Doc) Doc {
	if raw == nil {
		fail(400, "回传校验配置必须是对象")
	}
	enabled, ok := optional(raw, "enabled", false).(bool)
	if !ok {
		fail(400, "回传校验 enabled 必须是布尔值")
	}
	fields := array(optional(raw, "fields", []any{}))
	if fields == nil || len(fields) > 20 {
		fail(400, "回传校验 fields 必须是数组，最多 20 项")
	}
	if enabled && len(fields) == 0 {
		fail(400, "启用回传校验时至少需要一个字段")
	}
	seen := map[string]bool{}
	normalized := []any{}
	for _, value := range fields {
		field := object(value)
		if field == nil {
			fail(400, "回传校验字段必须是对象")
		}
		name := textField(field, "name", 64, true)
		if !fieldPattern.MatchString(name) || seen[name] {
			fail(400, "回传校验字段名格式不正确或重复")
		}
		seen[name] = true
		kind := textField(field, "type", 20, true)
		if kind != "string" && kind != "ip" && kind != "datetime" {
			fail(400, "回传校验字段类型只支持 string、ip 或 datetime")
		}
		required, ok := optional(field, "required", true).(bool)
		if !ok {
			fail(400, "回传校验 required 必须是布尔值")
		}
		maxLength, ok := number(optional(field, "max_length", 256))
		if !ok || maxLength < 1 || maxLength > 4096 {
			fail(400, "回传校验 max_length 必须是 1–4096 的整数")
		}
		if kind == "ip" && maxLength < 2 {
			fail(400, "IP 字段的 max_length 至少为 2")
		}
		if kind == "datetime" && maxLength < 20 {
			fail(400, "时间戳字段的 max_length 至少为 20")
		}
		normalized = append(normalized, Doc{"name": name, "type": kind, "required": required, "max_length": maxLength})
	}
	return Doc{"enabled": enabled, "fields": normalized}
}

func collectValidationConfig(workspace Doc) Doc {
	if config := object(workspace["collect_validation"]); config != nil {
		return clone(config)
	}
	return defaultCollectValidation()
}

// validateCollectedData does not mutate the request body. A passed schema only
// confirms field types and formats, including for entirely synthetic values.
func validateCollectedData(config, body Doc) Doc {
	if !boolean(config["enabled"]) {
		return nil
	}
	errors := []any{}
	addError := func(field, code, message string) {
		errors = append(errors, Doc{"field": field, "code": code, "message": message})
	}
	data := object(body["data"])
	if data == nil {
		addError("data", "invalid_type", "data 必须是 JSON 对象")
	} else {
		for _, value := range array(config["fields"]) {
			field := object(value)
			name, kind := str(field["name"]), str(field["type"])
			required := boolean(optional(field, "required", true))
			raw, exists := data[name]
			if !exists {
				if required {
					addError(name, "required", "缺少必填字段")
				}
				continue
			}
			text, ok := raw.(string)
			if !ok {
				addError(name, "invalid_type", "字段必须是字符串")
				continue
			}
			text = strings.TrimSpace(text)
			if text == "" {
				if required {
					addError(name, "required", "必填字段不能为空")
				}
				continue
			}
			if utf8.RuneCountInString(text) > integer(optional(field, "max_length", 256)) {
				addError(name, "max_length", "字段长度超过配置上限")
				continue
			}
			switch kind {
			case "ip":
				if net.ParseIP(text) == nil {
					addError(name, "invalid_ip", "字段必须是有效的 IPv4 或 IPv6 地址")
				}
			case "datetime":
				if !collectDateTime(text) {
					addError(name, "invalid_datetime", "字段必须是 RFC3339 时间戳")
				}
			}
		}
	}
	status := "passed"
	if len(errors) > 0 {
		status = "failed"
	}
	return Doc{"schema": status, "execution": "unverified", "errors": errors}
}

func collectDateTime(value string) bool {
	// time.Parse accepts some non-RFC3339 spellings, including one-digit hours,
	// comma fractions and out-of-range zone offsets. Reject those extensions.
	if len(value) < 20 || value[10] != 'T' || value[13] != ':' || value[16] != ':' {
		return false
	}
	zone := 19
	if value[zone] == '.' {
		zone++
		start := zone
		for zone < len(value) && value[zone] >= '0' && value[zone] <= '9' {
			zone++
		}
		if zone == start || zone == len(value) {
			return false
		}
	}
	if value[zone] == 'Z' {
		if zone != len(value)-1 {
			return false
		}
	} else {
		if len(value)-zone != 6 || (value[zone] != '+' && value[zone] != '-') || value[zone+3] != ':' {
			return false
		}
		if value[zone+1:zone+3] > "23" || value[zone+4:zone+6] > "59" {
			return false
		}
	}
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}
