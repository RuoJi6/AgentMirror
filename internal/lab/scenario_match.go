package lab

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type scenarioRequest struct {
	method, path, body    string
	query, headers, form  map[string][]string
	json                  any
	jsonOK                bool
	queryError, formError bool
}

func parseScenarioRequest(raw Doc) scenarioRequest {
	method := strings.ToUpper(str(optional(raw, "method", "GET")))
	p := str(raw["path"])
	query := map[string][]string{}
	queryError := false
	if i := strings.IndexByte(p, '?'); i >= 0 {
		parsed, err := url.ParseQuery(p[i+1:])
		query, queryError, p = parsed, err != nil, p[:i]
	}
	decodedPath, err := url.PathUnescape(p)
	if err != nil || !utf8.ValidString(decodedPath) || !strings.HasPrefix(decodedPath, "/") {
		fail(400, "场景请求路径格式无效")
	}
	p = decodedPath
	if value, exists := raw["raw_query"]; exists {
		parsed, err := url.ParseQuery(str(value))
		query, queryError = parsed, err != nil
	}
	if value, exists := raw["query"]; exists {
		query, queryError = scenarioValues(value, false), false
	}
	body := ""
	switch value := raw["body"].(type) {
	case string:
		body = value
	case []byte:
		body = string(value)
	}
	if len(body) > maxBody {
		fail(413, "场景请求正文不得超过 256 KiB")
	}
	headers := scenarioValues(raw["headers"], true)
	form := map[string][]string{}
	formError := false
	if value, exists := raw["form"]; exists {
		form = scenarioValues(value, false)
	} else if values := headers["content-type"]; len(values) > 0 {
		kind, params, err := mime.ParseMediaType(values[0])
		if err != nil {
			formError = true
		} else if kind == "application/x-www-form-urlencoded" {
			parsed, err := url.ParseQuery(body)
			form, formError = parsed, err != nil
		} else if kind == "multipart/form-data" {
			reader := multipart.NewReader(strings.NewReader(body), params["boundary"])
			for count := 0; ; count++ {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil || count >= 128 {
					formError = true
					break
				}
				value, err := io.ReadAll(part)
				part.Close()
				if err != nil {
					formError = true
					break
				}
				if key := part.FormName(); key != "" && part.FileName() == "" {
					form[key] = append(form[key], string(value))
				}
			}
		}
	}
	value, jsonOK := scenarioJSON(body)
	return scenarioRequest{method: method, path: p, body: body, query: query, headers: headers, form: form, json: value, jsonOK: jsonOK, queryError: queryError, formError: formError}
}

func scenarioValues(raw any, lowerKeys bool) map[string][]string {
	values := map[string][]string{}
	add := func(key string, value any) {
		if lowerKeys {
			key = strings.ToLower(key)
		}
		switch v := value.(type) {
		case string:
			values[key] = append(values[key], v)
		case []string:
			values[key] = append(values[key], v...)
		case []any:
			for _, item := range v {
				if text, ok := item.(string); ok {
					values[key] = append(values[key], text)
				}
			}
		}
	}
	switch v := raw.(type) {
	case map[string]any:
		for key, value := range v {
			add(key, value)
		}
	case map[string]string:
		for key, value := range v {
			add(key, value)
		}
	case map[string][]string:
		for key, value := range v {
			add(key, value)
		}
	case http.Header:
		for key, value := range v {
			add(key, value)
		}
	case url.Values:
		for key, value := range v {
			add(key, value)
		}
	}
	return values
}

// JSON keys use a simple dotted path (items.0.name); a leading slash accepts
// JSON Pointer, which can address literal dots and slashes in field names.
func scenarioJSONField(value any, key string) (any, bool) {
	parts := strings.Split(key, ".")
	if strings.HasPrefix(key, "/") {
		parts = strings.Split(key[1:], "/")
		for i, part := range parts {
			parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		}
	}
	for _, part := range parts {
		switch v := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = v[part]
			if !exists {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			value = v[i]
		default:
			return nil, false
		}
	}
	return value, true
}

func scenarioCondition(condition Doc, request scenarioRequest) Doc {
	source, key, operator := str(condition["source"]), str(condition["key"]), str(condition["operator"])
	values := []string{}
	exists, valid := false, true
	switch source {
	case "query":
		values, exists = request.query[key]
		valid = !request.queryError
	case "header":
		values, exists = request.headers[strings.ToLower(key)]
	case "form":
		values, exists = request.form[key]
		valid = !request.formError
	case "body":
		values, exists = []string{request.body}, operator != "exists" || request.body != ""
	case "json":
		valid = request.jsonOK
		if valid {
			value, found := scenarioJSONField(request.json, key)
			exists = found
			if found {
				switch v := value.(type) {
				case string:
					values = []string{v}
				case json.Number:
					values = []string{v.String()}
				default:
					values = []string{dump(v)}
				}
			}
		}
	default:
		valid = false
	}
	matched := false
	if valid && exists {
		if operator == "exists" {
			matched = true
		} else {
			for _, value := range values {
				if (operator == "equals" && value == str(condition["value"])) || (operator == "contains" && strings.Contains(value, str(condition["value"]))) {
					matched = true
					break
				}
			}
		}
	}
	reason := "value_mismatch"
	if !valid {
		reason = "invalid_request_data"
	} else if !exists {
		reason = "missing"
	} else if matched {
		reason = "matched"
	}
	return Doc{"source": source, "key": key, "operator": operator, "matched": matched, "reason": reason}
}

// evaluateScenarioRules is shared by published traffic and isolated previews.
// Rules are ordered. All matching routes are tried before selecting the first
// fallback, so a list rule's normal response cannot hide a later detail rule.
// The result's body is the selected representation even for HEAD; the caller
// suppresses wire bytes. HEAD never reports deliveries or changes state.
func evaluateScenarioRules(rules []Doc, rawRequest Doc, context Doc) Doc {
	if context == nil {
		context = Doc{}
	}
	request := parseScenarioRequest(rawRequest)
	method := request.method
	if method == "HEAD" {
		method = "GET"
	}
	trace := []any{}
	var fallbackRule Doc
	for _, rule := range rules {
		step := Doc{"binding_id": rule["binding_id"], "rule_id": rule["id"], "matched": false}
		if str(rule["path"]) != request.path {
			step["reason"] = "path_mismatch"
			trace = append(trace, step)
			continue
		}
		if str(rule["method"]) != method {
			step["reason"] = "method_mismatch"
			trace = append(trace, step)
			continue
		}
		if fallbackRule == nil && object(rule["fallback"]) != nil {
			fallbackRule = rule
		}
		conditions := []any{}
		matched := true
		for _, value := range array(rule["conditions"]) {
			check := scenarioCondition(object(value), request)
			conditions = append(conditions, check)
			matched = matched && boolean(check["matched"])
		}
		step["conditions"], step["matched"] = conditions, matched
		step["reason"] = "conditions_not_met"
		if matched {
			step["reason"] = "matched"
		}
		trace = append(trace, step)
		if matched {
			return renderScenarioResult(rule, object(rule["response"]), context, request.method, true, trace)
		}
	}
	if fallbackRule != nil {
		return renderScenarioResult(fallbackRule, object(fallbackRule["fallback"]), context, request.method, false, trace)
	}
	return Doc{"matched": false, "rule_matched": false, "deliveries": []any{}, "trace": trace}
}
