package lab

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

const scenarioBodyLimit = 128 * 1024
const scenarioOutputLimit = 2 * 1024 * 1024

var scenarioHeaderPattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
var scenarioSlotPattern = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// validateScenario normalizes only authored configuration. Bindings, profiles,
// request data and rendered instructions are supplied by the deployment layer.
func validateScenario(raw Doc) Doc {
	if raw == nil {
		fail(400, "场景必须是对象")
	}
	result := Doc{"name": textField(raw, "name", 80, true), "description": textField(raw, "description", 2000, false)}
	if id := str(raw["id"]); id != "" {
		if !idPattern.MatchString(id) {
			fail(400, "场景编号格式不正确")
		}
		result["id"] = id
	}
	version, ok := number(optional(raw, "version", 1))
	if !ok || version < 1 {
		fail(400, "场景版本必须是正整数")
	}
	result["version"] = version
	rules := array(raw["rules"])
	if len(rules) == 0 || len(rules) > 64 {
		fail(400, "场景需要 1–64 条规则")
	}
	seen := map[string]bool{}
	resultRules := []any{}
	for _, value := range rules {
		rule := object(value)
		if rule == nil {
			fail(400, "场景规则必须是对象")
		}
		id := textField(rule, "id", 64, true)
		if !idPattern.MatchString(id) || seen[id] {
			fail(400, "规则编号格式不正确或重复")
		}
		seen[id] = true
		method := strings.ToUpper(textField(merge(Doc{"method": "GET"}, rule), "method", 10, true))
		if method == "HEAD" {
			method = "GET"
		}
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		default:
			fail(400, "不支持的场景请求方法")
		}
		delivery, ok := optional(rule, "delivery_required", false).(bool)
		if !ok {
			fail(400, "delivery_required 必须是布尔值")
		}
		conditions := array(optional(rule, "conditions", []any{}))
		if _, exists := rule["conditions"]; exists && conditions == nil {
			fail(400, "规则条件必须是数组")
		}
		if len(conditions) > 32 {
			fail(400, "每条规则最多 32 个条件")
		}
		normalizedConditions := []any{}
		for _, value := range conditions {
			condition := object(value)
			if condition == nil {
				fail(400, "匹配条件必须是对象")
			}
			source := textField(condition, "source", 20, true)
			switch source {
			case "query", "header", "form", "json", "body":
			default:
				fail(400, "未知匹配条件来源")
			}
			key := textField(condition, "key", 200, source != "body")
			if source == "body" && key != "" {
				fail(400, "body 条件不需要 key")
			}
			if source == "header" {
				if !scenarioHeaderPattern.MatchString(key) {
					fail(400, "请求头字段名无效")
				}
				key = strings.ToLower(key)
			}
			operator := textField(condition, "operator", 20, true)
			if operator != "equals" && operator != "contains" && operator != "exists" {
				fail(400, "未知匹配操作符")
			}
			normalized := Doc{"source": source, "key": key, "operator": operator}
			if operator != "exists" {
				value, ok := condition["value"].(string)
				if !ok || len(value) > 8192 {
					fail(400, "匹配值必须是长度不超过 8192 字节的字符串")
				}
				normalized["value"] = value
			}
			normalizedConditions = append(normalizedConditions, normalized)
		}
		normalized := Doc{"id": id, "name": textField(rule, "name", 100, false), "method": method, "path": validateScenarioPath(textField(rule, "path", 1000, true)), "conditions": normalizedConditions, "delivery_required": delivery}
		normalized["response"] = validateScenarioResponse(object(rule["response"]), delivery)
		if value, exists := rule["fallback"]; exists && value != nil {
			normalized["fallback"] = validateScenarioResponse(object(value), false)
		}
		resultRules = append(resultRules, normalized)
	}
	result["rules"] = resultRules
	return result
}

// Deployments must use this same validator for binding path overrides.
func validateScenarioPath(value string) string {
	u, err := url.ParseRequestURI(value)
	if err != nil || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.ForceQuery || strings.ContainsAny(value, "?#%\\") || u.Path != value {
		fail(400, "规则路径必须是绝对路径，不包含域名、查询参数或转义字符")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			fail(400, "规则路径不能包含控制字符")
		}
	}
	clean := path.Clean(value)
	if clean != value && clean+"/" != value {
		fail(400, "规则路径不能包含重复斜线或相对路径段")
	}
	if value == "/collect" || value == "/health" || value == "/__am" || strings.HasPrefix(value, "/__am/") {
		fail(400, "规则路径占用了系统保留接口")
	}
	return value
}

func validateScenarioResponse(raw Doc, delivery bool) Doc {
	if raw == nil {
		fail(400, "规则响应必须是对象")
	}
	status, ok := number(optional(raw, "status", 200))
	if !ok || status < 200 || status > 599 {
		fail(400, "响应状态码必须是 200–599 的整数")
	}
	format := str(optional(raw, "format", "text"))
	types := map[string]string{"text": "text/plain; charset=utf-8", "html": "text/html; charset=utf-8", "json": "application/json; charset=utf-8"}
	if types[format] == "" {
		fail(400, "响应格式只支持 text、html 或 json")
	}
	prefix, ok := optional(raw, "prompt_prefix", "").(string)
	if !ok || len(prefix) > 32 || (prefix != "" && format != "text") {
		fail(400, "prompt_prefix 仅适用于文本响应，且不得超过 32 字节")
	}
	for _, r := range prefix {
		if !strings.ContainsRune(" \t#/*;!-", r) {
			fail(400, "prompt_prefix 只能包含空格、制表符或 # / * ; ! - 注释字符")
		}
	}
	contentType := str(optional(raw, "content_type", types[format]))
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || len(contentType) > 200 || strings.ContainsAny(contentType, "\r\n") {
		fail(400, "响应 Content-Type 无效")
	}
	if charset := params["charset"]; charset != "" && !strings.EqualFold(charset, "utf-8") {
		fail(400, "场景响应只支持 UTF-8 字符集")
	}
	isJSON := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	if (format == "json" && !isJSON) || (format != "json" && isJSON) || (format == "html" && mediaType != "text/html") || (format != "html" && mediaType == "text/html") {
		fail(400, "响应格式与 Content-Type 不一致")
	}
	body, ok := raw["body"].(string)
	if !ok || len(body) > scenarioBodyLimit || !utf8.ValidString(body) {
		fail(400, "响应正文必须是 UTF-8 字符串且不超过 128 KiB")
	}
	if (status == 204 || status == 205 || status == 304) && (body != "" || delivery) {
		fail(400, "无正文状态码不能包含正文或投放提示词")
	}
	if format == "json" && body != "" {
		value, valid := scenarioJSON(body)
		if !valid {
			fail(400, "JSON 响应正文必须是完整有效的 JSON")
		}
		body = dump(normalizeScenarioJSON(value))
	} else {
		body = normalizeScenarioSlots(body)
	}
	slots := strings.Count(body, "{{prompt}}")
	if slots > 32 {
		fail(400, "每个响应最多包含 32 个提示词槽位")
	}
	if delivery && slots == 0 {
		fail(400, "此响应已启用提示词交付，但正文没有 {{prompt}} 槽位；普通返回请关闭交付，交付提示词请在正文插入槽位")
	}
	if !delivery && slots > 0 {
		fail(400, "普通响应不能包含 {{prompt}} 槽位；请移除槽位，需要交付提示词时请启用交付")
	}
	if format == "html" && slots > 0 && !scenarioHTMLTextSlots(body) {
		fail(400, "HTML 提示词槽位必须放在文本节点，不能放入标签、属性、脚本、样式或注释")
	}
	headers := Doc{}
	if rawHeaders := optional(raw, "headers", Doc{}); rawHeaders != nil {
		values := object(rawHeaders)
		if values == nil || len(values) > 32 {
			fail(400, "响应头必须是对象且最多 32 项")
		}
		for key, value := range values {
			name := http.CanonicalHeaderKey(key)
			if !scenarioHeaderPattern.MatchString(key) || len(key) > 100 || headers[name] != nil {
				fail(400, "响应头字段名无效或重复")
			}
			switch strings.ToLower(key) {
			case "content-type", "content-length", "content-encoding", "transfer-encoding", "connection", "trailer", "te", "upgrade", "keep-alive", "set-cookie", "x-run-id", "x-run-token", "content-security-policy", "x-frame-options", "x-content-type-options":
				fail(400, "响应头由系统管理，不能覆盖："+name)
			}
			v, ok := value.(string)
			if !ok || len(v) > 4096 || !utf8.ValidString(v) {
				fail(400, "响应头值必须是长度不超过 4096 字节的字符串")
			}
			for _, r := range v {
				if (r < 0x20 && r != '\t') || r == 0x7f {
					fail(400, "响应头值不能包含换行或控制字符")
				}
			}
			if strings.Contains(v, "{{") || strings.Contains(v, "}}") {
				fail(400, "响应头不支持模板槽位")
			}
			headers[name] = v
		}
	}
	return Doc{"status": status, "format": format, "content_type": contentType, "body": body, "headers": headers, "prompt_prefix": prefix}
}

func normalizeScenarioSlots(body string) string {
	result := scenarioSlotPattern.ReplaceAllStringFunc(body, func(slot string) string {
		if strings.TrimSpace(slot[2:len(slot)-2]) != "prompt" {
			fail(400, "响应中存在未知槽位："+slot)
		}
		return "{{prompt}}"
	})
	remaining := strings.ReplaceAll(result, "{{prompt}}", "")
	if strings.Contains(remaining, "{{") {
		fail(400, "响应中存在不完整或未知槽位")
	}
	return result
}

func scenarioJSON(body string) (any, bool) {
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	return value, decoder.Decode(&extra) == io.EOF
}

func normalizeScenarioJSON(value any) any {
	switch v := value.(type) {
	case string:
		return normalizeScenarioSlots(v)
	case map[string]any:
		for key, child := range v {
			if strings.Contains(key, "{{") {
				fail(400, "JSON 槽位只能位于字符串值，不能位于字段名")
			}
			v[key] = normalizeScenarioJSON(child)
		}
	case []any:
		for i, child := range v {
			v[i] = normalizeScenarioJSON(child)
		}
	}
	return value
}

// Check contexts with the same HTML tokenization rules a browser uses. This
// does not sanitize authored markup; only inserted prompt text is escaped.
func scenarioHTMLTextSlots(body string) bool {
	z := xhtml.NewTokenizer(strings.NewReader(body))
	unsafeRaw, seenSlots := "", 0
	for {
		kind := z.Next()
		if kind == xhtml.ErrorToken {
			return z.Err() == io.EOF && seenSlots == strings.Count(body, "{{prompt}}")
		}
		slots := strings.Count(string(z.Raw()), "{{prompt}}")
		if slots > 0 && (kind != xhtml.TextToken || unsafeRaw != "") {
			return false
		}
		seenSlots += slots
		token := z.Token()
		if kind == xhtml.StartTagToken || kind == xhtml.SelfClosingTagToken {
			switch token.Data {
			case "script", "style", "xmp", "iframe", "noembed", "noframes", "plaintext":
				unsafeRaw = token.Data
			}
		} else if kind == xhtml.EndTagToken && token.Data == unsafeRaw {
			unsafeRaw = ""
		}
	}
}
