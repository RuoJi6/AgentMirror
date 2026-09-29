package lab

import (
	"bytes"
	"html"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func scenarioFixture() Doc {
	return Doc{"name": "Contract fixture", "rules": []any{Doc{"id": "read", "method": "GET", "path": "/fixture", "conditions": []any{}, "response": Doc{"status": 200, "format": "text", "body": "Output: {{prompt}}"}, "delivery_required": true}}}
}

func scenarioFixtureRule(raw Doc) Doc { return object(array(raw["rules"])[0]) }

func scenarioCompiled(raw Doc, prompt string) []Doc {
	validated := validateScenario(raw)
	rules := []Doc{}
	for _, value := range array(validated["rules"]) {
		rule := clone(object(value))
		rule["binding_id"] = "test-binding"
		rule["profile"] = Doc{"id": "test-profile", "version": 3, "body": prompt, "fields": []any{}, "commands": []any{}}
		rules = append(rules, rule)
	}
	return rules
}

func TestScenarioFileParameterBranches(t *testing.T) {
	rules := scenarioCompiled(fileParameterScenario(), "BOUND_PROMPT_MARKER")
	for _, tc := range []struct {
		name, target, rule, contains string
		status                       int
		delivery                     bool
	}{
		{"homepage source", "/api/download?file=index.html", "home-source", "<!doctype html>", 200, false},
		{"readme", "/api/download?file=README.md", "readme", "index.html", 200, false},
		{"target literal", "/api/download?file=../.././etc/passwd", "target-file", "BOUND_PROMPT_MARKER", 200, true},
		{"target encoded", "/api/download?file=..%2F..%2F.%2Fetc%2Fpasswd", "target-file", "BOUND_PROMPT_MARKER", 200, true},
		{"different traversal spelling", "/api/download?file=../../etc/passwd", "home-source", "File not found", 404, false},
		{"different file", "/api/download?file=../.././etc/hosts", "home-source", "File not found", 404, false},
		{"double encoding", "/api/download?file=..%252F..%252F.%252Fetc%252Fpasswd", "home-source", "File not found", 404, false},
		{"missing file", "/api/download", "home-source", "File not found", 404, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := evaluateScenarioRules(rules, Doc{"method": "GET", "path": tc.target}, nil)
			if result["rule_id"] != tc.rule || integer(result["status"]) != tc.status || !strings.Contains(str(result["body"]), tc.contains) {
				t.Fatalf("unexpected branch response: %v", result)
			}
			if boolean(result["contains_prompt"]) != tc.delivery || (len(array(result["deliveries"])) == 1) != tc.delivery || strings.Contains(str(result["body"]), "BOUND_PROMPT_MARKER") != tc.delivery {
				t.Fatalf("prompt must only appear in selected branch: %v", result)
			}
		})
	}
	// Editing the default does not shadow a later, more specific file rule.
	object(rules[0]["fallback"])["body"] = "CUSTOM_UNKNOWN_FILE"
	unknown := evaluateScenarioRules(rules, Doc{"path": "/api/download?file=unknown"}, nil)
	if unknown["body"] != "CUSTOM_UNKNOWN_FILE" || boolean(unknown["rule_matched"]) {
		t.Fatal("custom default response was ignored", unknown)
	}
	target := evaluateScenarioRules(rules, Doc{"path": "/api/download?file=../.././etc/passwd"}, nil)
	if target["rule_id"] != "target-file" {
		t.Fatal("fallback shadowed the target branch", target)
	}
	if result := evaluateScenarioRules(rules, Doc{"method": "POST", "path": "/api/download?file=../.././etc/passwd"}, nil); boolean(result["matched"]) {
		t.Fatal("wrong HTTP method matched", result)
	}
}

func TestScenarioNacosListDetailAndFallback(t *testing.T) {
	var preset Doc
	for _, item := range scenarioPresets() {
		if item["id"] == "nacos-config-read" {
			preset = item
		}
	}
	if preset == nil {
		t.Fatal("Nacos preset missing")
	}
	rules := scenarioCompiled(preset, "FIXTURE {{run_id}}")
	context := Doc{"run_id": "test-run", "token": "token", "callback_url": "http://localhost/collect"}
	list := evaluateScenarioRules(rules, Doc{"method": "GET", "path": "/nacos/v1/cs/configs?search=accurate"}, context)
	if !boolean(list["matched"]) || list["rule_id"] != "config-list" || len(array(list["deliveries"])) != 0 {
		t.Fatalf("incorrect list response: %v", list)
	}
	page := decode(str(list["body"]))
	config := object(array(page["pageItems"])[0])
	detailPath := "/nacos/v1/cs/configs?dataId=" + str(config["dataId"]) + "&group=" + str(config["group"])
	detail := evaluateScenarioRules(rules, Doc{"method": "GET", "path": detailPath}, context)
	if detail["rule_id"] != "config-detail" || !boolean(detail["rule_matched"]) || !strings.Contains(str(detail["body"]), "FIXTURE test-run") || len(array(detail["deliveries"])) != 1 {
		t.Fatalf("list fallback hid detail or prompt missing: %v", detail)
	}
	fallback := evaluateScenarioRules(rules, Doc{"method": "GET", "path": "/nacos/v1/cs/configs?dataId=missing&group=DEFAULT_GROUP"}, context)
	if !boolean(fallback["matched"]) || boolean(fallback["rule_matched"]) || integer(fallback["status"]) != 404 || len(array(fallback["deliveries"])) != 0 {
		t.Fatalf("incorrect normal fallback: %v", fallback)
	}
	for _, request := range []Doc{{"method": "GET", "path": "/nacos/v1/cs/configs/missing"}, {"method": "POST", "path": detailPath}} {
		if result := evaluateScenarioRules(rules, request, context); boolean(result["matched"]) {
			t.Fatalf("method/path must match exactly: %v", result)
		}
	}
}

func TestScenarioRenderingIsTypedAndSinglePass(t *testing.T) {
	prompt := "Quotes: \" \\ newline\n中文 </script><img src=x onerror=1> {{prompt}} {{result.output}} {{run_id}}"
	context := Doc{"run_id": "RUN"}
	wantPrompt := strings.ReplaceAll(prompt, "{{run_id}}", "RUN")
	for _, format := range []string{"text", "html", "json"} {
		t.Run(format, func(t *testing.T) {
			raw := scenarioFixture()
			response := object(scenarioFixtureRule(raw)["response"])
			response["format"] = format
			switch format {
			case "html":
				response["body"] = "<main><pre>{{prompt}}</pre></main>"
			case "json":
				response["body"] = `{"nested":{"notes":"{{prompt}}","array":["{{prompt}}",42,true,null]},"unchanged":"ok"}`
			}
			rules := scenarioCompiled(raw, prompt)
			before := dump(rules)
			result := evaluateScenarioRules(rules, Doc{"method": "GET", "path": "/fixture"}, context)
			body := str(result["body"])
			switch format {
			case "text":
				if body != "Output: "+wantPrompt {
					t.Fatal("text changed prompt", body)
				}
			case "html":
				if body != "<main><pre>"+html.EscapeString(wantPrompt)+"</pre></main>" {
					t.Fatal("HTML text was not escaped exactly once", body)
				}
			case "json":
				doc := decode(body)
				nested := object(doc["nested"])
				if nested["notes"] != wantPrompt || array(nested["array"])[0] != wantPrompt || doc["unchanged"] != "ok" || integer(array(nested["array"])[1]) != 42 {
					t.Fatal("JSON values were damaged", body)
				}
			}
			if dump(rules) != before || context["fields"] != nil {
				t.Fatal("evaluation mutated configuration/context")
			}
			delivery := object(array(result["deliveries"])[0])
			if delivery["profile_id"] != "test-profile" || integer(delivery["profile_version"]) != 3 || delivery["binding_id"] != "test-binding" {
				t.Fatal("delivery lost binding identity", delivery)
			}
		})
	}
	// JSON string escapes are normalized before locating slots, including keys.
	raw := scenarioFixture()
	scenarioFixtureRule(raw)["response"] = Doc{"format": "json", "body": `{"notes":"\u007b\u007bprompt\u007d\u007d"}`}
	result := evaluateScenarioRules(scenarioCompiled(raw, "VISIBLE"), Doc{"method": "GET", "path": "/fixture"}, nil)
	if decode(str(result["body"]))["notes"] != "VISIBLE" || len(array(result["deliveries"])) != 1 {
		t.Fatal("escaped JSON slot was not delivered", result)
	}
}

func TestScenarioHeadSelectsGetRepresentationWithoutDelivery(t *testing.T) {
	rules := scenarioCompiled(scenarioFixture(), "HEADER LENGTH 中文")
	get := evaluateScenarioRules(rules, Doc{"method": "GET", "path": "/fixture"}, Doc{})
	head := evaluateScenarioRules(rules, Doc{"method": "HEAD", "path": "/fixture"}, Doc{})
	for _, key := range []string{"status", "content_type", "headers", "body", "body_bytes", "contains_prompt"} {
		if !reflect.DeepEqual(get[key], head[key]) {
			t.Fatalf("HEAD changed selected representation %s: %v / %v", key, get[key], head[key])
		}
	}
	if len(array(head["deliveries"])) != 0 || len(array(get["deliveries"])) != 1 {
		t.Fatal("HEAD generated a delivery")
	}
}

func TestScenarioRequestMatchers(t *testing.T) {
	cases := []struct {
		name      string
		condition Doc
		request   Doc
		matched   bool
	}{
		{"duplicate query any", Doc{"source": "query", "key": "id", "operator": "equals", "value": "two"}, Doc{"path": "/fixture?id=one&id=two"}, true},
		{"header casing any", Doc{"source": "header", "key": "X-MODE", "operator": "contains", "value": "demo"}, Doc{"headers": http.Header{"X-Mode": []string{"other", "demo-value"}}}, true},
		{"form encoding", Doc{"source": "form", "key": "label", "operator": "equals", "value": "two words"}, Doc{"headers": Doc{"Content-Type": "application/x-www-form-urlencoded"}, "body": "label=one&label=two+words"}, true},
		{"json number", Doc{"source": "json", "key": "items.0.number", "operator": "equals", "value": "42"}, Doc{"body": `{"items":[{"number":42}]}`}, true},
		{"json null exists", Doc{"source": "json", "key": "token", "operator": "exists"}, Doc{"body": `{"token":null}`}, true},
		{"json pointer", Doc{"source": "json", "key": "/a.b/c~1d", "operator": "equals", "value": "ok"}, Doc{"body": `{"a.b":{"c/d":"ok"}}`}, true},
		{"raw body", Doc{"source": "body", "operator": "contains", "value": "marker"}, Doc{"body": "hello marker"}, true},
		{"empty body equality", Doc{"source": "body", "operator": "equals", "value": ""}, Doc{}, true},
		{"empty body absent", Doc{"source": "body", "operator": "exists"}, Doc{}, false},
		{"missing query", Doc{"source": "query", "key": "missing", "operator": "exists"}, Doc{}, false},
		{"invalid query fails closed", Doc{"source": "query", "key": "id", "operator": "equals", "value": "ok"}, Doc{"path": "/fixture?id=ok&bad=%xy"}, false},
		{"invalid form fails closed", Doc{"source": "form", "key": "id", "operator": "equals", "value": "ok"}, Doc{"headers": Doc{"content-type": "application/x-www-form-urlencoded"}, "body": "id=ok&bad=%xy"}, false},
		{"trailing json", Doc{"source": "json", "key": "id", "operator": "equals", "value": "ok"}, Doc{"body": `{"id":"ok"} {}`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := scenarioFixture()
			rule := scenarioFixtureRule(raw)
			rule["method"] = "POST"
			rule["conditions"] = []any{tc.condition, Doc{"source": "header", "key": "X-Required", "operator": "exists"}}
			request := merge(Doc{"method": "POST", "path": "/fixture"}, tc.request)
			headers := scenarioValues(request["headers"], false)
			headers["X-Required"] = []string{""}
			request["headers"] = headers
			result := evaluateScenarioRules(scenarioCompiled(raw, "PROMPT"), request, nil)
			if boolean(result["matched"]) != tc.matched {
				t.Fatalf("unexpected match: %v", result)
			}
			delete(headers, "X-Required")
			if failed := evaluateScenarioRules(scenarioCompiled(raw, "PROMPT"), request, nil); boolean(failed["matched"]) {
				t.Fatal("conditions must all match")
			}
		})
	}
}

func TestScenarioMultipartFieldsAndInstanceIsolation(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("name", "demo"); err != nil {
		t.Fatal(err)
	}
	file, err := w.CreateFormFile("name", "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	file.Write([]byte("file bytes"))
	w.Close()
	raw := scenarioFixture()
	rule := scenarioFixtureRule(raw)
	rule["method"] = "POST"
	rule["conditions"] = []any{Doc{"source": "form", "key": "name", "operator": "equals", "value": "demo"}}
	first := scenarioCompiled(raw, "FIRST")
	second := scenarioCompiled(raw, "SECOND")
	second[0]["binding_id"], second[0]["path"] = "second", "/other"
	rules := append(first, second...)
	request := Doc{"method": "POST", "path": "/other", "headers": Doc{"Content-Type": w.FormDataContentType()}, "body": body.String()}
	result := evaluateScenarioRules(rules, request, nil)
	if result["binding_id"] != "second" || str(result["body"]) != "Output: SECOND" {
		t.Fatal("binding or multipart field mixed", result)
	}
	// File contents do not masquerade as a normal form field of the same name.
	object(array(second[0]["conditions"])[0])["value"] = "file bytes"
	if result := evaluateScenarioRules(second, request, nil); boolean(result["matched"]) {
		t.Fatal("uploaded file matched a form field condition")
	}
}

func TestScenarioConfigurationRejectsInvalidContracts(t *testing.T) {
	cases := []struct {
		name string
		edit func(Doc, Doc, Doc)
	}{
		{"reserved collect", func(d, r, p Doc) { r["path"] = "/collect" }},
		{"reserved health", func(d, r, p Doc) { r["path"] = "/health" }},
		{"reserved namespace", func(d, r, p Doc) { r["path"] = "/__am/test" }},
		{"path query", func(d, r, p Doc) { r["path"] = "/fixture?id=1" }},
		{"path traversal", func(d, r, p Doc) { r["path"] = "/foo/../fixture" }},
		{"path encoded", func(d, r, p Doc) { r["path"] = "/%66ixture" }},
		{"unsupported method", func(d, r, p Doc) { r["method"] = "TRACE" }},
		{"duplicate id", func(d, r, p Doc) { d["rules"] = append(array(d["rules"]), clone(r)) }},
		{"null conditions", func(d, r, p Doc) { r["conditions"] = nil }},
		{"unsupported condition", func(d, r, p Doc) {
			r["conditions"] = []any{Doc{"source": "query", "key": "id", "operator": "exec", "value": "x"}}
		}},
		{"missing prompt", func(d, r, p Doc) { p["body"] = "no slot" }},
		{"undeclared prompt", func(d, r, p Doc) { r["delivery_required"] = false }},
		{"unknown slot", func(d, r, p Doc) { p["body"] = "{{prompt}} {{token}}" }},
		{"unclosed slot", func(d, r, p Doc) { p["body"] = "{{prompt}} {{broken" }},
		{"prompt in fallback", func(d, r, p Doc) { r["fallback"] = clone(p) }},
		{"no-body status", func(d, r, p Doc) { p["status"] = 204 }},
		{"interim status", func(d, r, p Doc) { p["status"] = 103 }},
		{"JSON invalid", func(d, r, p Doc) { p["format"], p["body"] = "json", `{"value":{{prompt}}}` }},
		{"JSON key slot", func(d, r, p Doc) { p["format"], p["body"] = "json", `{"{{prompt}}":"value"}` }},
		{"JSON escaped unknown", func(d, r, p Doc) { p["format"], p["body"] = "json", `{"a":"{{prompt}}","b":"\u007b\u007bunknown}}"}` }},
		{"MIME mismatch", func(d, r, p Doc) { p["content_type"] = "application/json" }},
		{"charset mismatch", func(d, r, p Doc) { p["content_type"] = "text/plain; charset=latin1" }},
		{"header newline", func(d, r, p Doc) { p["headers"] = Doc{"X-Test": "a\r\nInjected: yes"} }},
		{"header name", func(d, r, p Doc) { p["headers"] = Doc{"X-Test\nBad": "value"} }},
		{"header framing", func(d, r, p Doc) { p["headers"] = Doc{"Content-Length": "2"} }},
		{"header cookie", func(d, r, p Doc) { p["headers"] = Doc{"Set-Cookie": "session=other"} }},
		{"header duplicate", func(d, r, p Doc) { p["headers"] = Doc{"X-Test": "1", "x-test": "2"} }},
		{"header slot", func(d, r, p Doc) { p["headers"] = Doc{"X-Test": "{{prompt}}"} }},
		{"too many slots", func(d, r, p Doc) { p["body"] = strings.Repeat("{{prompt}}", 33) }},
		{"prefix newline", func(d, r, p Doc) { p["prompt_prefix"] = "#\n" }},
		{"prefix expression", func(d, r, p Doc) { p["prompt_prefix"] = "{{prompt}}" }},
		{"prefix too long", func(d, r, p Doc) { p["prompt_prefix"] = strings.Repeat("#", 33) }},
		{"prefix html", func(d, r, p Doc) { p["format"], p["prompt_prefix"] = "html", "# " }},
	}
	for _, markup := range []string{`<div title="{{prompt}}">Text</div>`, `<script>{{prompt}}</script>`, `<style>{{prompt}}</style>`, `<!-- {{prompt}} -->`, `<script>bad</scriptx>{{prompt}}</script>`, `<script/src=x>{{prompt}}</script>`, `<script/>{{prompt}}</script>`, `<iframe>{{prompt}}</iframe>`, `<div title="{{prompt}}`} {
		value := markup
		cases = append(cases, struct {
			name string
			edit func(Doc, Doc, Doc)
		}{"HTML context " + value, func(d, r, p Doc) { p["format"], p["body"] = "html", value }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p, ok := recover().(problem)
				if !ok || p.status != 400 {
					t.Fatalf("expected a 400 validation failure, got %v", p)
				}
			}()
			raw := scenarioFixture()
			rule := scenarioFixtureRule(raw)
			tc.edit(raw, rule, object(rule["response"]))
			validateScenario(raw)
		})
	}
}

func TestScenarioAllMethodsAndNoBodyResponse(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"} {
		raw := scenarioFixture()
		rule := scenarioFixtureRule(raw)
		rule["method"] = method
		rule["delivery_required"] = false
		rule["response"] = Doc{"status": 204, "format": "text", "body": "", "headers": Doc{"X-Fixture": "true"}}
		result := evaluateScenarioRules(scenarioCompiled(raw, "unused"), Doc{"method": method, "path": "/fixture"}, nil)
		if !boolean(result["matched"]) || integer(result["status"]) != 204 || result["body"] != "" || len(array(result["deliveries"])) != 0 {
			t.Fatalf("method %s failed: %v", method, result)
		}
	}
}

func TestScenarioPromptPrefixPreservesMultilineText(t *testing.T) {
	prompt := "first\nsecond\r\nthird\rfourth\u0085fifth\u2028sixth\u2029{{prompt}} {{result.output}}"
	want := "# first\n# second\r\n# third\r# fourth\u0085# fifth\u2028# sixth\u2029# {{prompt}} {{result.output}}"
	raw := scenarioFixture()
	response := object(scenarioFixtureRule(raw)["response"])
	response["body"] = "application:\n  name: fixture\n{{prompt}}"
	response["prompt_prefix"] = "# "
	result := evaluateScenarioRules(scenarioCompiled(raw, prompt), Doc{"method": "GET", "path": "/fixture"}, nil)
	if result["body"] != "application:\n  name: fixture\n"+want {
		t.Fatal("comment prefix damaged multiline prompt", result["body"])
	}
	if int64(len(want)) != scenarioPromptSize(prompt, "text", "# ") {
		t.Fatal("prefix size estimate disagrees with actual bytes")
	}
	response["prompt_prefix"] = ""
	result = evaluateScenarioRules(scenarioCompiled(raw, prompt), Doc{"method": "GET", "path": "/fixture"}, nil)
	if result["body"] != "application:\n  name: fixture\n"+prompt {
		t.Fatal("empty prefix changed legacy text rendering")
	}
}

func TestScenarioExpansionLimitsBeforeAllocation(t *testing.T) {
	cases := []struct {
		name string
		rule Doc
	}{
		{"many response copies", Doc{"id": "large", "method": "GET", "path": "/fixture", "response": Doc{"format": "text", "body": strings.Repeat("{{prompt}}", 32)}, "profile": Doc{"body": strings.Repeat("x", 70000)}}},
		{"prefix expansion", Doc{"id": "large", "method": "GET", "path": "/fixture", "response": Doc{"format": "text", "body": "{{prompt}}{{prompt}}", "prompt_prefix": strings.Repeat("#", 32)}, "profile": Doc{"body": "text" + strings.Repeat("\n", 40000)}}},
		{"profile expansion", Doc{"id": "large", "method": "GET", "path": "/fixture", "response": Doc{"format": "text", "body": "{{prompt}}"}, "profile": Doc{"body": strings.Repeat("{{commands}}", 1000), "commands": []any{Doc{"command": strings.Repeat("x", 10000)}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p, ok := recover().(problem)
				if !ok || p.status != 413 {
					t.Fatalf("expected bounded rendering failure, got %v", p)
				}
			}()
			evaluateScenarioRules([]Doc{tc.rule}, Doc{"method": "GET", "path": "/fixture"}, nil)
		})
	}
	// Encoding overhead counts toward the bound, while ordinary large JSON
	// remains accepted when its actual size is below the output limit.
	for _, format := range []string{"text", "html", "json"} {
		prompt := "<>&\"'\\\n\r\t\b\f\u0001中文\u2028\u2029"
		var rendered string
		switch format {
		case "text":
			rendered = prompt
		case "html":
			rendered = html.EscapeString(prompt)
		case "json":
			rendered = dump(prompt)
			rendered = rendered[1 : len(rendered)-1]
		}
		if int64(len(rendered)) != scenarioPromptSize(prompt, format, "") {
			t.Fatalf("%s size estimate mismatch", format)
		}
	}
}

func TestScenarioPathsDecodeOnceAndQueryValuesStayOnceDecoded(t *testing.T) {
	raw := scenarioFixture()
	rule := scenarioFixtureRule(raw)
	rule["path"] = "/配置"
	rule["conditions"] = []any{Doc{"source": "query", "key": "name", "operator": "equals", "value": "%2F"}}
	rules := scenarioCompiled(raw, "PROMPT")
	result := evaluateScenarioRules(rules, Doc{"method": "GET", "path": "/" + url.PathEscape("配置") + "?name=%252F"}, nil)
	if !boolean(result["matched"]) {
		t.Fatal("Unicode path did not match or query was decoded twice", result)
	}
	rule["path"] = "/nested/path"
	rule["conditions"] = []any{}
	rules = scenarioCompiled(raw, "PROMPT")
	if result := evaluateScenarioRules(rules, Doc{"method": "GET", "path": "/nested%252Fpath"}, nil); boolean(result["matched"]) {
		t.Fatal("encoded slash decoded twice")
	}
	defer func() {
		p, ok := recover().(problem)
		if !ok || p.status != 400 {
			t.Fatal("malformed path should fail with 400", p)
		}
	}()
	evaluateScenarioRules(rules, Doc{"method": "GET", "path": "/bad%zz"}, nil)
}
