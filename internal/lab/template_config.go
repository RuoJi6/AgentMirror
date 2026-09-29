package lab

import "strings"

// The template remains a data document: HTML/CSS are served to the browser,
// never evaluated as a server-side template language.
func templateConfig(raw, old Doc) Doc {
	doc := Doc{}
	for _, key := range []string{"brand", "title", "description"} {
		doc[key] = textField(raw, key, 500, true)
	}
	doc["accent"] = textField(raw, "accent", 7, true)
	if !colorPattern.MatchString(str(doc["accent"])) {
		fail(400, "颜色必须为六位十六进制值")
	}
	mode := str(optional(raw, "render_mode", fallback(old["render_mode"], "builtin")))
	if mode != "builtin" && mode != "custom" {
		fail(400, "未知模板编辑模式")
	}
	doc["render_mode"] = mode
	for key, limit := range map[string]int{"html": 96 * 1024, "css": 32 * 1024} {
		value := optional(raw, key, fallback(old[key], ""))
		content, ok := value.(string)
		if !ok || len(content) > limit {
			fail(400, key+" 内容格式错误或超过大小限制")
		}
		doc[key] = content
	}
	if mode == "custom" {
		if strings.TrimSpace(str(doc["html"])) == "" {
			fail(400, "请填写自定义 HTML 页面正文")
		}
		if raw["kind"] == "login" && !strings.Contains(str(doc["html"]), "{{login_form}}") {
			fail(400, "自定义登录页面需要包含 {{login_form}}，用于模拟登录")
		}
	}
	tags := optional(raw, "tags", fallback(old["tags"], []any{}))
	values, ok := tags.([]any)
	if !ok || len(values) > 12 {
		fail(400, "标签必须为数组，最多 12 个")
	}
	normalized := []any{}
	seen := map[string]bool{}
	for _, value := range values {
		tag, ok := value.(string)
		if !ok {
			fail(400, "标签必须是文字")
		}
		tag = textField(Doc{"tag": tag}, "tag", 32, true)
		key := strings.ToLower(tag)
		if !seen[key] {
			normalized = append(normalized, tag)
			seen[key] = true
		}
	}
	doc["tags"] = normalized
	return doc
}

const previewPolicy = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"

const customLoginForm = `<form id="login-form"><label>工作邮箱<input type="email" required placeholder="name@company.com" autocomplete="off"></label><label>测试密码<input type="password" required placeholder="输入测试密码" autocomplete="off"></label><button class="primary" type="submit">登录工作空间</button><p id="form-result" role="status"></p></form>`

func customTemplateBody(template Doc) string {
	body := str(template["html"])
	// Keep delivery connected even when a user removes the optional placement slots.
	if !strings.Contains(body, "{{delivery}}") {
		body += "{{delivery}}"
	}
	if !strings.Contains(body, "{{resources}}") {
		body += "{{resources}}"
	}
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>AMBRANDPLACEHOLDER · AMTITLEPLACEHOLDER</title><style>:root{--accent:AMACCENTPLACEHOLDER}*{box-sizing:border-box}body{margin:0;font:15px/1.6 system-ui,sans-serif;color:#20232b;background:#fff}input,button{font:inherit}#login-form label{display:block;margin:14px 0}#login-form input{display:block;width:100%;padding:10px;margin-top:6px;border:1px solid #dce0e8;border-radius:6px}.primary{background:var(--accent);color:#fff;border:0;border-radius:6px;padding:10px 18px;cursor:pointer}.preview-bar{padding:9px;text-align:center;background:#fff9db;color:#785b2b;font-size:12px}.delivery-note{margin:24px;padding:20px;border:1px solid #ddd;white-space:pre-wrap;overflow-wrap:anywhere}</style><style>AMCSSPLACEHOLDER</style></head><body>AMPREVIEWPLACEHOLDER` + body + `</body></html>`
}
