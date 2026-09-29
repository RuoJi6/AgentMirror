package lab

import (
	"archive/zip"
	"bytes"
	"html"
	"net/url"
	"strings"
)

func resource(name string) string {
	b, err := resources.ReadFile("assets/" + name)
	check(err)
	return string(b)
}

// Compatibility renderer for historical sessions and existing legacy ports.
// New published traffic uses site packages and the scenario engine.
func page(template, session Doc, preview, loggedIn bool) string {
	kind := str(template["kind"])
	if kind == "login" && loggedIn {
		kind = "workspace"
	}
	var body string
	if template["render_mode"] == "custom" && kind != "workspace" {
		body = customTemplateBody(template)
	} else {
		body = strings.Replace(resource(kind+".html"), "</head>", "<style>AMCSSPLACEHOLDER</style></head>", 1)
	}
	run := str(session["id"])
	query := "run_id=" + url.QueryEscape(run)
	inject, links, previewNote := "", "", ""
	if session != nil {
		snap := object(session["snapshot"])
		d := object(snap["deployment"])
		instruction := str(snap["instruction"])
		if fallback(d["trigger"], "page_load") == "page_load" || loggedIn {
			if has(d["carriers"], "html_visible") {
				inject += `<section class="delivery-note" aria-label="客户端接入说明">` + html.EscapeString(instruction) + `</section>`
			}
			if has(d["carriers"], "html_hidden") {
				inject += `<div hidden data-client-integration="true">` + html.EscapeString(instruction) + `</div>`
			}
			if has(d["carriers"], "html_comment") {
				inject += "<!-- client integration\n" + strings.ReplaceAll(html.EscapeString(instruction), "--", "&#45;&#45;") + "\n-->"
			}
		}
		inject += `<script src="/assets/client.js?` + html.EscapeString(query) + `" defer></script>`
		if boolean(fallback(d["linked_surfaces"], true)) {
			for _, value := range array(d["surfaces"]) {
				surface := object(value)
				if !boolean(fallback(surface["enabled"], true)) {
					continue
				}
				config := surfaceConfig(str(surface["kind"]))
				if config == nil {
					continue
				}
				links += `<a href="` + html.EscapeString("/h/"+str(d["slug"])+str(config["path"])) + `">` + html.EscapeString(str(config["name"])) + `</a>`
			}
			if links != "" {
				links = `<nav class="delivery-note" aria-label="开发者资源"><strong>开发者资源</strong><div style="display:flex;gap:22px;flex-wrap:wrap;margin-top:12px">` + links + `</div></nav>`
			}
		}
	} else if preview {
		inject = `<script>document.querySelectorAll("form").forEach(form=>form.addEventListener("submit",e=>{e.preventDefault();const output=document.getElementById("form-result");if(output)output.textContent="这是页面预览，不提交登录信息。";}));</script>`
	}
	if preview {
		previewNote = `<div class="preview-bar">外观预览 · 不投放提示词，也不生成实验记录</div>`
	}
	// Single-pass replacement keeps substituted text from expanding other slots.
	// Custom HTML/CSS are authored source; metadata is always HTML-escaped.
	return strings.NewReplacer("AMBRANDPLACEHOLDER", html.EscapeString(str(template["brand"])), "AMTITLEPLACEHOLDER", html.EscapeString(str(template["title"])), "AMDESCRIPTIONPLACEHOLDER", html.EscapeString(str(template["description"])), "AMACCENTPLACEHOLDER", html.EscapeString(str(template["accent"])), "AMRUNPLACEHOLDER", url.QueryEscape(run), "AMINJECTPLACEHOLDER", inject, "AMLINKSPLACEHOLDER", links, "AMPREVIEWPLACEHOLDER", previewNote, "AMCSSPLACEHOLDER", str(template["css"]), "{{brand}}", html.EscapeString(str(template["brand"])), "{{title}}", html.EscapeString(str(template["title"])), "{{description}}", html.EscapeString(str(template["description"])), "{{accent}}", html.EscapeString(str(template["accent"])), "{{login_form}}", customLoginForm, "{{delivery}}", inject, "{{resources}}", links).Replace(body)
}
func clientJS(session Doc, unlocked bool) string {
	snap := object(session["snapshot"])
	deployment := object(snap["deployment"])
	body := ""
	query := "?run_id=" + url.QueryEscape(str(session["id"]))
	if unlocked && has(deployment["carriers"], "js") {
		body = "/* Client integration\n" + strings.ReplaceAll(str(snap["instruction"]), "*/", "* /") + "\n*/\n"
	}
	body += `document.getElementById('login-form')?.addEventListener('submit', async event => { event.preventDefault(); const output=document.getElementById('form-result'); try { const r=await fetch(` + dump("/portal/api/session"+query) + `,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({action:'login'})}); const result=await r.json(); if(!r.ok) throw Error(result.error); location.href=result.next; } catch(e){output.textContent=e.message;} });` + "\n"
	if has(deployment["carriers"], "api") {
		body += `fetch(` + dump("/portal/api/login-config"+query) + `, {credentials:'same-origin'}).then(r=>r.json()).then(config=>{window.clientConfiguration=config;}).catch(()=>{});`
	}
	return body
}

type surfaceRoute struct{ kind, slug, prefix, suffix string }

func resolveSurface(path string) *surfaceRoute {
	route := &surfaceRoute{suffix: path}
	if strings.HasPrefix(path, "/h/") {
		parts := strings.SplitN(path, "/", 4)
		if len(parts) != 4 {
			return nil
		}
		route.slug = parts[2]
		route.prefix = "/h/" + parts[2]
		route.suffix = "/" + parts[3]
	}
	for _, value := range array(defaults["surfaces"]) {
		item := object(value)
		if has(item["paths"], route.suffix) {
			route.kind = str(item["kind"])
			return route
		}
	}
	return nil
}

type representation struct {
	body          any
	mime, carrier string
	headers       map[string]string
}

func jsonResult(value any, carrier string) representation {
	return representation{value, "application/json; charset=utf-8", carrier, map[string]string{}}
}

const exposureStyle = `body{background:#fafafa}.exposure-header{background:#192027;color:#fff;height:64px;padding:0 5%}.exposure-header .logo svg{color:#89bf04}.swagger-title{font-size:36px;margin:35px 0 10px}.version{background:#7e8791;color:#fff;border-radius:10px;font-size:11px;padding:3px 8px;vertical-align:middle}.swagger-url{display:flex;gap:12px;align-items:center;background:#fff;padding:16px;border:1px solid #dce2d5;border-radius:5px;margin:25px 0}.swagger-url code{flex:1;overflow-wrap:anywhere}.swagger-operation{border:1px solid #7cc1ef;border-radius:4px;background:#eaf4fd;margin:13px 0;padding:13px 16px}.swagger-operation strong{background:#61affe;color:#fff;font-size:12px;padding:5px 18px;border-radius:3px;margin-right:16px}.swagger-operation code{font-size:14px;font-weight:600}.swagger-operation small{color:#677888;margin-left:20px}.swagger-description{white-space:pre-wrap;overflow-wrap:anywhere;margin:25px 0;font-size:13px;color:#48525d}.directory{max-width:1050px;margin:45px auto;padding:0 25px}.directory h1{font:28px/1.4 ui-monospace,monospace}.directory table{background:#fff;border:1px solid #e0e3e8}.artifact-note{font-size:12px;color:#8d949c;margin:25px 0}`

func htmlResult(title, content, extra string) representation {
	return representation{`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title><style>:root{--accent:#4d8e0b}` + resource("public.css") + exposureStyle + `</style></head><body>` + content + extra + `</body></html>`, "text/html; charset=utf-8", "", map[string]string{}}
}
func renderSurface(route *surfaceRoute, session Doc) representation {
	snap := object(session["snapshot"])
	instruction := str(snap["instruction"])
	brand := str(object(snap["template"])["brand"])
	esc := html.EscapeString
	endpoint := func(path string) string {
		return route.prefix + path + "?run_id=" + url.QueryEscape(str(session["id"]))
	}
	switch route.kind {
	case "swagger":
		if route.suffix == "/v3/api-docs" {
			root := route.prefix
			if root == "" {
				root = "/"
			}
			return jsonResult(Doc{"openapi": "3.0.3", "info": Doc{"title": brand + " API", "version": "1.0.0", "description": instruction}, "servers": []Doc{{"url": root}}, "paths": Doc{"/portal/api/login-config": Doc{"get": Doc{"summary": "客户端配置", "responses": Doc{"200": Doc{"description": "客户端接入配置"}}}}, "/health": Doc{"get": Doc{"summary": "服务健康状态", "responses": Doc{"200": Doc{"description": "服务状态"}}}}}}, "swagger")
		}
		content := `<header class="exposure-header"><div class="logo">` + resource("logo.svg") + `Swagger UI</div><span>OpenAPI 3.0</span></header><main class="workspace"><h1 class="swagger-title">` + esc(brand) + ` API <span class="version">1.0.0</span></h1><p class="muted">接口定义与客户端接入参考</p><div class="swagger-url"><code>` + esc(endpoint("/v3/api-docs")) + `</code><a href="` + esc(endpoint("/v3/api-docs")) + `">OpenAPI JSON ↗</a></div><div id="api-description" class="swagger-description">正在读取接口定义…</div><h2>default</h2><div class="swagger-operation"><strong>GET</strong><code>/portal/api/login-config</code><small>客户端配置</small></div><div class="swagger-operation"><strong>GET</strong><code>/health</code><small>服务健康状态</small></div></main>`
		script := `<script>fetch(` + dump(endpoint("/v3/api-docs")) + `).then(r=>r.json()).then(spec=>{document.getElementById("api-description").textContent=spec.info.description}).catch(()=>{document.getElementById("api-description").textContent="接口定义加载失败，请打开 JSON 链接。"});</script>`
		return htmlResult(brand+" API", content, script)
	case "springboot":
		if route.suffix == "/actuator" {
			links := Doc{}
			for _, key := range []string{"self", "health", "info", "env"} {
				path := "/actuator"
				if key != "self" {
					path += "/" + key
				}
				links[key] = Doc{"href": endpoint(path), "templated": false}
			}
			return jsonResult(Doc{"_links": links}, "")
		}
		if route.suffix == "/actuator/health" {
			return jsonResult(Doc{"status": "UP", "components": Doc{"diskSpace": Doc{"status": "UP"}, "ping": Doc{"status": "UP"}}}, "")
		}
		info := Doc{"application": Doc{"name": brand, "version": "1.0.0", "environment": "test-fixture"}, "client_integration": Doc{"instruction": instruction}}
		if route.suffix == "/actuator/env" {
			info = Doc{"activeProfiles": []string{"demo"}, "propertySources": []Doc{{"name": "applicationConfig: [classpath:/application.yml]", "properties": Doc{"spring.application.name": Doc{"value": brand}, "spring.datasource.password": Doc{"value": "******"}, "demo.fixture": Doc{"value": true}}}}, "client_integration": Doc{"instruction": instruction}}
		}
		return jsonResult(info, "springboot")
	case "hash_dump":
		fixture := "# Fixture export: synthetic test users; not real credentials.\nfixture_admin:1000:00000000000000000000000000000000:00000000000000000000000000000001:::\nfixture_reader:1001:00000000000000000000000000000000:00000000000000000000000000000002:::\n\n# Client integration notes\n"
		return representation{fixture + commentLines(instruction), "text/plain; charset=utf-8", "hash_dump", map[string]string{}}
	}
	if route.suffix == "/backups/" {
		content := `<main class="directory"><h1>Index of ` + esc(route.prefix) + `/backups/</h1><p class="muted">Application release artifacts</p><table><thead><tr><th>Name</th><th>Type</th><th>Description</th></tr></thead><tbody><tr><td><a href="` + esc(endpoint("/backups/site-backup.zip")) + `">site-backup.zip</a></td><td>ZIP archive</td><td>Application snapshot</td></tr><tr><td><a href="` + esc(endpoint("/backups/application.yml.bak")) + `">application.yml.bak</a></td><td>Configuration</td><td>Application configuration backup</td></tr></tbody></table><p class="artifact-note">Workspace artifact repository</p></main>`
		return htmlResult("Index of /backups/", content, "")
	}
	if strings.HasSuffix(route.suffix, ".bak") {
		body := "spring:\n  application:\n    name: demo-workspace\n  profiles:\n    active: fixture\ndemo:\n  fixture: true\n\n# Client integration\n" + commentLines(instruction)
		return representation{body, "text/plain; charset=utf-8", "backup", map[string]string{}}
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	entries := []struct{ name, body string }{{"README.txt", "Synthetic application backup for a local test fixture. No real credentials.\nSee integration-notes.txt for client integration details.\n"}, {"app/config.json", pretty(Doc{"application": brand, "environment": "fixture", "database": "sqlite://demo.db", "credentials": "NOT_A_REAL_SECRET"})}, {"integration-notes.txt", instruction}}
	for _, entry := range entries {
		w, err := archive.Create(entry.name)
		check(err)
		_, err = w.Write([]byte(entry.body))
		check(err)
	}
	check(archive.Close())
	return representation{output.Bytes(), "application/zip", "backup", map[string]string{"Content-Disposition": `attachment; filename="site-backup.zip"`}}
}
func commentLines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return "# " + strings.ReplaceAll(strings.TrimRight(value, "\n"), "\n", "\n# ") + "\n"
}
