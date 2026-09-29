package lab

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const composerMaxBody = 16 * 1024 * 1024

// Package imports have a separate bound; collection endpoints retain maxBody.
func composerBody(w http.ResponseWriter, r *http.Request) Doc {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(415, "请使用 application/json")
	}
	reader := http.MaxBytesReader(w, r.Body, composerMaxBody)
	defer reader.Close()
	b, err := io.ReadAll(reader)
	if err != nil {
		fail(413, "站点包请求不得超过 16 MiB")
	}
	if !utf8.Valid(b) {
		fail(400, "需要 UTF-8 JSON")
	}
	var doc Doc
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	if decoder.Decode(&doc) != nil || doc == nil {
		fail(400, "需要 JSON 对象")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		fail(400, "JSON 后存在多余内容")
	}
	return doc
}

func entityMaybe(q queryer, kind, id string) Doc {
	found := rows(q, "SELECT doc FROM entities WHERE kind=? AND id=?", kind, id)
	if len(found) == 0 {
		return nil
	}
	return decode(str(found[0]["doc"]))
}

func versionKey(doc Doc) string { return str(doc["id"]) + "-v" + strconv.Itoa(integer(doc["version"])) }

func (s *Store) saveComposer(kind string, raw Doc, guards ...func(queryer)) Doc {
	var result Doc
	s.write(func(q queryer) { result = saveComposerIn(q, kind, raw, guards...) })
	if kind == "workspaces" {
		return workspaceView(s.db, result)
	}
	return result
}

// Reuse validation, versioning and live publication inside compound writes.
func saveComposerIn(q queryer, kind string, raw Doc, guards ...func(queryer)) Doc {
	var normalized Doc
	switch kind {
	case "sites":
		normalized = validateSite(raw)
	case "scenarios":
		normalized = validateScenario(raw)
	case "workspaces":
		normalized = clone(raw)
	default:
		fail(404, "未知组合素材")
	}
	id := str(raw["id"])
	if id == "" {
		id = randomHex(6)
	}
	if !idPattern.MatchString(id) {
		fail(400, "记录编号格式不正确")
	}
	var result Doc
	for _, guard := range guards {
		guard(q)
	}
	old := entityMaybe(q, kind, id)
	if str(raw["id"]) != "" && old == nil {
		fail(404, "素材不存在，请作为新素材保存")
	}
	if old != nil && integer(raw["version"]) != integer(old["version"]) {
		fail(409, "草稿已更新，请刷新后重试")
	}
	result = normalized
	if kind == "sites" {
		validateHostedSiteFiles(q, result)
	}
	if kind == "workspaces" {
		slug := textField(raw, "slug", 60, true)
		if !slugPattern.MatchString(slug) {
			fail(400, "路径仅支持小写字母、数字和连字符")
		}
		for _, item := range listing(q, "workspaces") {
			if item["slug"] == slug && item["id"] != id {
				fail(409, "工作区路径已被使用")
			}
		}
		siteID := textField(raw, "site_id", 64, false)
		if siteID != "" {
			get(q, "sites", siteID)
		}
		mounts := normalizeSiteMounts(q, optional(raw, "site_mounts", old["site_mounts"]))
		bindings := normalizeBindings(q, raw["bindings"])
		rules := compileWorkspaceBindings(q, Doc{"bindings": bindings, "site_mounts": mounts})
		callback := validateCallbackPath(textField(raw, "callback_path", 256, false))
		if _, supplied := raw["callback_path"]; !supplied {
			callback = callbackPath(old)
		}
		validateWorkspaceSiteRoutes(q, Doc{"deployment_id": old["deployment_id"], "callback_path": callback, "site_id": siteID, "site_mounts": mounts}, rules)
		rejectedResponse := collectRejectedResponseConfig(old)
		if value, supplied := raw["collect_rejected_response"]; supplied {
			rejectedResponse = validateCollectResponse(object(value))
		}
		validation := collectValidationConfig(old)
		if value, supplied := raw["collect_validation"]; supplied {
			validation = validateCollectValidation(object(value))
		}
		serverHeader := workspaceServerHeader(old)
		if _, supplied := raw["server_header"]; supplied {
			serverHeader = validateServerHeader(raw)
		}
		result = Doc{"site_mounts": mounts, "server_header": serverHeader, "slug": slug, "site_id": siteID, "bindings": bindings, "callback_path": callback, "collect_rejected_response": rejectedResponse, "collect_validation": validation}
		for _, k := range []string{"deployment_id", "published_version", "published_at", "publication_revision"} {
			if old[k] != nil {
				result[k] = old[k]
			}
		}
	}
	merge(result, Doc{"id": id, "name": textField(raw, "name", 80, true), "version": integer(old["version"]) + 1, "created_at": fallback(old["created_at"], timestamp()), "updated_at": timestamp()})
	put(q, kind, result)
	if kind == "sites" || kind == "scenarios" {
		immutable := clone(result)
		immutable["id"] = versionKey(result)
		immutable["source_id"] = id
		put(q, kind+"_versions", immutable)
	}
	syncSavedMaterials(q, kind, id)
	if kind == "workspaces" {
		result = get(q, kind, id)
	}
	return result
}

func normalizeBindings(q queryer, raw any) []any {
	if raw == nil {
		return []any{}
	}
	values := array(raw)
	if values == nil || len(values) > 32 {
		fail(400, "场景实例必须为数组，最多 32 个")
	}
	result := []any{}
	seen := map[string]bool{}
	for _, value := range values {
		b := object(value)
		if b == nil {
			fail(400, "场景实例格式错误")
		}
		id := str(b["id"])
		if id == "" {
			id = randomHex(6)
		}
		if !idPattern.MatchString(id) || seen[id] {
			fail(400, "场景实例编号必须有效且唯一")
		}
		seen[id] = true
		scenarioID := textField(b, "scenario_id", 64, true)
		scenario := get(q, "scenarios", scenarioID)
		profileID := textField(b, "profile_id", 64, false)
		if profileID != "" {
			get(q, "profiles", profileID)
		}
		ruleProfiles := Doc{}
		if b["rule_profiles"] != nil {
			if object(b["rule_profiles"]) == nil {
				fail(400, "接口提示词绑定必须为对象")
			}
			ruleIDs := map[string]bool{}
			for _, rawRule := range array(scenario["rules"]) {
				ruleIDs[str(object(rawRule)["id"])] = true
			}
			for ruleID, value := range object(b["rule_profiles"]) {
				if !ruleIDs[ruleID] {
					fail(400, "接口提示词绑定引用了不存在的规则："+ruleID)
				}
				selected := textField(Doc{"profile_id": value}, "profile_id", 64, false)
				if selected != "" {
					get(q, "profiles", selected)
					ruleProfiles[ruleID] = selected
				}
			}
		}
		paths := Doc{}
		if b["paths"] != nil {
			if object(b["paths"]) == nil {
				fail(400, "接口映射必须为对象")
			}
			for key, v := range object(b["paths"]) {
				found := false
				for _, r := range array(scenario["rules"]) {
					if object(r)["id"] == key {
						found = true
					}
				}
				if !found {
					fail(400, "接口映射引用了不存在的规则："+key)
				}
				p, ok := v.(string)
				if !ok {
					fail(400, "接口映射路径必须为文字")
				}
				if strings.TrimSpace(p) != "" {
					paths[key] = p
				}
			}
		}
		enabled, ok := optional(b, "enabled", true).(bool)
		if !ok {
			fail(400, "实例启用状态必须为布尔值")
		}
		item := Doc{"id": id, "scenario_id": scenarioID, "profile_id": profileID, "rule_profiles": ruleProfiles, "paths": paths, "enabled": enabled}
		if node := textField(b, "site_node_id", 64, false); node != "" && node != workspaceRootSite {
			item["site_node_id"] = node
		}
		result = append(result, item)
	}
	return result
}

// An explicit rule assignment takes precedence over the instance default.
// Empty or omitted entries inherit, including workspaces saved before this field existed.
func bindingProfileID(binding Doc, ruleID string) string {
	if id := str(object(binding["rule_profiles"])[ruleID]); id != "" {
		return id
	}
	return str(binding["profile_id"])
}

func compileBindings(q queryer, bindings []any) []Doc {
	compiled := []Doc{}
	seen := map[string]string{}
	for bindingIndex, value := range bindings {
		b := object(value)
		if !boolean(b["enabled"]) {
			continue
		}
		scenario := clone(get(q, "scenarios", str(b["scenario_id"])))
		for _, raw := range array(scenario["rules"]) {
			rule := object(raw)
			if mapped := str(object(b["paths"])[str(rule["id"])]); mapped != "" {
				rule["path"] = mapped
			}
		}
		scenario = validateScenario(scenario)
		for _, raw := range array(scenario["rules"]) {
			rule := clone(object(raw))
			var profile Doc
			if id := bindingProfileID(b, str(rule["id"])); id != "" {
				profile = get(q, "profiles", id)
			}
			if boolean(rule["delivery_required"]) && profile == nil {
				fail(400, "请为接口「"+str(rule["name"])+"」选择提示词，或设置场景默认提示词")
			}
			key := str(rule["method"]) + ":" + str(rule["path"]) + ":" + dump(rule["conditions"])
			owner := fmt.Sprintf("实例 %d「%s」", bindingIndex+1, str(scenario["name"]))
			if previous := seen[key]; previous != "" {
				fail(409, "场景规则重复："+str(rule["method"])+" "+str(rule["path"])+" 同时出现在 "+previous+" 和 "+owner+"。请在绑定提示词中移除重复实例，或设置不同的接口映射/触发条件。")
			}
			seen[key] = owner
			merge(rule, Doc{"binding_id": b["id"], "scenario_id": scenario["id"], "scenario_name": scenario["name"], "scenario_version": scenario["version"], "profile": profile})
			if str(b["site_node_id"]) != "" {
				rule["site_node_id"] = b["site_node_id"]
			}
			compiled = append(compiled, rule)
		}
	}
	if len(compiled) > 256 {
		fail(400, "每个工作区最多 256 条规则")
	}
	return compiled
}

func (s *Store) publishWorkspace(id string, expectedVersion int) Doc {
	var deployment Doc
	s.write(func(q queryer) {
		workspace := get(q, "workspaces", id)
		if expectedVersion > 0 && expectedVersion != integer(workspace["version"]) {
			fail(409, "草稿已更新，请刷新后再发布")
		}
		if str(workspace["site_id"]) == "" {
			fail(400, "请先为工作区选择或生成站点，再发布")
		}
		site := get(q, "sites", str(workspace["site_id"]))
		bindings := normalizeBindings(q, workspace["bindings"])
		rules := compileWorkspaceBindings(q, workspace)
		validateWorkspaceSiteRoutes(q, workspace, rules)
		mounts := workspaceSiteVersions(q, workspace)
		depID := str(workspace["deployment_id"])
		if depID == "" {
			depID = "composer-" + id
		}
		for _, d := range listing(q, "deployments") {
			if d["slug"] == workspace["slug"] && d["id"] != depID {
				fail(409, "发布路径已被其他部署使用")
			}
		}
		previous := entityMaybe(q, "deployments", depID)
		revision := max(integer(previous["revision"]), integer(workspace["publication_revision"])) + 1
		releaseID := depID + "-r" + strconv.Itoa(revision)
		put(q, "composer_releases", Doc{"id": releaseID, "site_mounts": mounts, "site_version_id": versionKey(site), "rules": rules, "bindings": bindings, "server_header": workspaceServerHeader(workspace), "callback_path": callbackPath(workspace), "collect_rejected_response": collectRejectedResponseConfig(workspace), "collect_validation": collectValidationConfig(workspace), "created_at": timestamp()})
		deployment = Doc{"id": depID, "name": workspace["name"], "slug": workspace["slug"], "mode": "composable", "workspace_id": id, "release_id": releaseID, "revision": revision, "site_id": site["id"], "site_version": site["version"], "server_header": workspaceServerHeader(workspace), "callback_path": callbackPath(workspace), "collect_rejected_response": collectRejectedResponseConfig(workspace), "collect_validation": collectValidationConfig(workspace), "enabled": true, "created_at": fallback(previous["created_at"], timestamp()), "updated_at": timestamp()}
		put(q, "deployments", deployment)
		merge(workspace, Doc{"deployment_id": depID, "published_version": workspace["version"], "published_at": timestamp(), "publication_revision": revision})
		put(q, "workspaces", workspace)
	})
	return deployment
}

func (s *Store) deleteComposer(kind, id string, expected ...int) {
	s.write(func(q queryer) {
		current := get(q, kind, id)
		if len(expected) > 0 && integer(current["version"]) != expected[0] {
			fail(409, "素材已更新，请重新读取后删除")
		}
		for _, workspace := range listing(q, "workspaces") {
			if kind == "sites" && workspaceUsesSite(workspace, id) {
				fail(409, "站点仍被工作区引用")
			}
			if kind == "scenarios" {
				for _, b := range array(workspace["bindings"]) {
					if object(b)["scenario_id"] == id {
						fail(409, "场景仍被工作区引用")
					}
				}
			}
		}
		if kind == "workspaces" {
			fail(400, "请通过工作区生命周期接口删除，以同步暂停和解绑端口")
		}
		// Immutable material versions stay available for published and historical runs.
		exec(q, "DELETE FROM entities WHERE kind=? AND id=?", kind, id)
	})
}

func (a *App) composerRoute(w http.ResponseWriter, r *http.Request) bool {
	p, method := r.URL.Path, r.Method
	send := func(v any) { response(w, r, v, 200, "", nil) }
	if p == "/api/composer/state" && method == "GET" {
		send(Doc{"sites": listing(a.store.db, "sites"), "scenarios": listing(a.store.db, "scenarios"), "workspaces": workspaceListing(a.store.db), "presets": Doc{"sites": sitePresets(), "scenarios": scenarioPresets()}})
		return true
	}
	if p == "/api/sites/import" && method == "POST" {
		raw := composerBody(w, r)
		b, err := base64.StdEncoding.DecodeString(str(raw["zip_base64"]))
		if err != nil {
			fail(400, "ZIP 编码错误")
		}
		send(a.store.saveComposer("sites", importSiteZIP(b, textField(raw, "name", 80, true))))
		return true
	}
	if p == "/api/sites/preview" && method == "POST" {
		raw := composerBody(w, r)
		site := object(raw["site"])
		if site == nil {
			site = raw
		}
		send(Doc{"html": sitePreviewHTML(validateSite(site))})
		return true
	}
	if p == "/api/composer/preview" && method == "POST" {
		raw := composerBody(w, r)
		var result Doc
		a.store.write(func(q queryer) {
			raw["site_mounts"] = normalizeSiteMounts(q, raw["site_mounts"])
			rules := compileWorkspaceBindings(q, raw)
			snapshot := Doc{"site_mounts": workspaceSiteVersions(q, raw), "rules": rules, "server_header": validateServerHeader(raw)}
			if str(raw["site_id"]) != "" {
				snapshot["site_version_id"] = versionKey(get(q, "sites", str(raw["site_id"])))
			}
			snapshot = listenerSiteRelease(snapshot, Doc{"site_node_id": textField(raw, "site_node_id", 64, false)})
			rules = nil
			for _, item := range array(snapshot["rules"]) {
				rules = append(rules, object(item))
			}
			request := object(raw["request"])
			if request == nil {
				fail(400, "请提供预演请求")
			}
			result = evaluateScenarioRules(rules, request, Doc{"run_id": "PREVIEW_RUN", "token": "PREVIEW_TOKEN", "callback_url": "http://preview.invalid" + validateCallbackPath(textField(raw, "callback_path", 256, false))})
			if !boolean(result["matched"]) && str(raw["site_id"]) != "" {
				u, _ := url.ParseRequestURI(str(request["path"]))
				if u == nil {
					fail(400, "请求路径无效")
				}
				site, node, local := selectWorkspaceSite(q, snapshot, u.Path)
				if str(node["server_header"]) != "" {
					applyWorkspaceServer(result, node)
				}
				if str(request["method"]) == "GET" || str(request["method"]) == "HEAD" || str(request["method"]) == "" {
					u, err := url.ParseRequestURI(str(request["path"]))
					if err == nil {
						if asset := hostedSiteFile(site, local); asset != nil {
							meta := get(q, "hosted_files", str(asset["file_id"]))
							merge(result, Doc{"matched": true, "status": 200, "content_type": "application/octet-stream", "body": "", "source": "hosted_file", "download": meta})
						} else if location := mountedSiteEntryLocation(site, node, u); location != "" {
							merge(result, Doc{"matched": true, "status": 307, "content_type": "text/plain; charset=utf-8", "body": "", "headers": Doc{"Location": location}, "source": "site_redirect"})
						} else if b, mime, found := siteResponse(site, local); found {
							merge(result, Doc{"matched": true, "status": 200, "content_type": mime, "body": string(b), "source": "site"})
						}
					}
				}
			}
			if !boolean(result["matched"]) {
				merge(result, Doc{"status": 404, "content_type": "text/plain; charset=utf-8", "body": "Not found"})
			}
			raw["server_header"] = validateServerHeader(raw)
			u, _ := url.ParseRequestURI(str(request["path"]))
			if u != nil {
				applySiteServer(result, snapshot, u.Path)
			}
			if str(request["method"]) == "HEAD" {
				result["body"] = ""
			}
		})
		send(result)
		return true
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) >= 2 && parts[0] == "api" && (parts[1] == "sites" || parts[1] == "scenarios" || parts[1] == "workspaces") {
		kind := parts[1]
		if len(parts) == 2 && method == "GET" {
			if kind == "workspaces" {
				send(workspaceListing(a.store.db))
			} else {
				send(listing(a.store.db, kind))
			}
			return true
		}
		if len(parts) == 2 && method == "POST" {
			send(a.store.saveComposer(kind, composerBody(w, r)))
			return true
		}
		if len(parts) == 3 && method == "GET" {
			doc := get(a.store.db, kind, parts[2])
			if kind == "workspaces" {
				doc = workspaceView(a.store.db, doc)
			}
			send(doc)
			return true
		}
		if len(parts) == 3 && method == "DELETE" {
			if kind == "workspaces" {
				send(a.listeners.deleteWorkspace(parts[2]))
			} else {
				a.store.deleteComposer(kind, parts[2])
				send(Doc{"ok": true})
			}
			return true
		}
		if len(parts) == 4 && kind == "workspaces" && parts[3] == "publish" && method == "POST" {
			raw := composerBody(w, r)
			send(a.store.publishWorkspace(parts[2], workspaceExpectedVersion(raw)))
			return true
		}
		if len(parts) == 4 && kind == "workspaces" && parts[3] == "status" && method == "POST" {
			send(a.store.workspaceStatus(parts[2], composerBody(w, r)))
			return true
		}
		if len(parts) == 4 && kind == "workspaces" && parts[3] == "rename" && method == "POST" {
			send(a.store.renameWorkspace(parts[2], composerBody(w, r)))
			return true
		}
		if len(parts) >= 4 && kind == "workspaces" && parts[3] == "collect-rejected-response" {
			workspace := get(a.store.db, "workspaces", parts[2])
			if len(parts) == 4 && method == "GET" {
				response(w, r, collectRejectedResponseConfig(workspace), 200, "", nil)
				return true
			}
			if len(parts) == 4 && method == "POST" {
				raw := requestBody(w, r)
				if workspaceExpectedVersion(raw) != integer(workspace["version"]) {
					fail(409, "工作区已更新，请刷新后再修改令牌失效响应")
				}
				workspace["collect_rejected_response"] = validateCollectResponse(raw)
				response(w, r, a.store.saveComposer("workspaces", workspace), 200, "", nil)
				return true
			}
			if len(parts) == 5 && parts[4] == "preview" && method == "POST" {
				config := validateCollectResponse(requestBody(w, r))
				response(w, r, renderCollectRejectedResponse(config, "PREVIEW_RUN"), 200, "", nil)
				return true
			}
		}
		if len(parts) == 4 && kind == "workspaces" && parts[3] == "collect-validation" {
			workspace := get(a.store.db, "workspaces", parts[2])
			if method == "GET" {
				send(collectValidationConfig(workspace))
				return true
			}
			if method == "POST" {
				raw := requestBody(w, r)
				if workspaceExpectedVersion(raw) != integer(workspace["version"]) {
					fail(409, "工作区已更新，请刷新后再修改回传字段校验")
				}
				workspace["collect_validation"] = validateCollectValidation(raw)
				send(a.store.saveComposer("workspaces", workspace))
				return true
			}
		}
	}
	return false
}

func (s *Store) composerSession(r *http.Request, listener Doc) (Doc, Doc) {
	var session, site Doc
	s.write(func(q queryer) {
		config := get(q, "listeners", str(listener["id"]))
		if !boolean(config["enabled"]) || integer(config["port"]) != integer(listener["port"]) {
			fail(404, "端口已停用")
		}
		deployment := get(q, "deployments", str(config["deployment_id"]))
		if !boolean(deployment["enabled"]) {
			fail(404, "部署已停用")
		}
		run := r.URL.Query().Get("run_id")
		headerRun := r.Header.Get("X-Run-ID")
		if run != "" && headerRun != "" && run != headerRun {
			fail(400, "会话标识冲突")
		}
		if run == "" {
			run = headerRun
		}
		explicit := run != ""
		if run == "" {
			if c, e := r.Cookie("am_run_" + str(listener["id"])); e == nil {
				run = c.Value
			}
		}
		if run != "" {
			found := rows(q, "SELECT id,snapshot FROM sessions WHERE id=?", run)
			if len(found) > 0 {
				snap := decode(str(found[0]["snapshot"]))
				compatible := object(snap["deployment"])["id"] == deployment["id"] && object(snap["deployment"])["mode"] == "composable" && object(snap["listener"])["id"] == listener["id"] && integer(object(snap["listener"])["port"]) == integer(listener["port"]) && listenerSiteID(object(snap["listener"])) == listenerSiteID(config)
				if compatible {
					session = Doc{"id": run, "snapshot": snap}
					rotateSessionToken(q, session)
					refreshSessionMaterials(q, session, deployment)
					snap["callback_url"] = listenerCallbackURL(config, deployment)
					refreshSessionPrompts(q, session)
				} else if explicit {
					fail(403, "会话不属于当前端口或部署")
				}
			} else if explicit {
				fail(404, "会话不存在")
			}
		}
		if session == nil {
			release := listenerSiteRelease(get(q, "composer_releases", str(deployment["release_id"])), config)
			id, token := randomHex(8), randomToken(24)
			callback := listenerCallbackURL(config, deployment)
			profile := Doc{"name": "未绑定页面提示词", "version": 0, "body": ""}
			for _, rule := range array(release["rules"]) {
				if p := object(object(rule)["profile"]); p != nil {
					profile = p
					break
				}
			}
			snap := Doc{"deployment": deployment, "release_id": release["id"], "site_version_id": release["site_version_id"], "site_mounts": release["site_mounts"], "rules": release["rules"], "profile": profile, "template": Doc{"kind": "files", "name": deployment["name"], "brand": deployment["name"]}, "token": token, "callback_url": callback, "listener": Doc{"id": config["id"], "name": config["name"], "port": config["port"], "public_url": config["public_url"], "host": config["host"], "site_node_id": listenerSiteID(config)}}
			snap["root_site_node_id"], snap["server_header"] = listenerSiteID(config), release["server_header"]
			session = Doc{"id": id, "snapshot": snap}
			refreshSessionPrompts(q, session)
			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			ua := []rune(r.UserAgent())
			if len(ua) > 1000 {
				ua = ua[:1000]
			}
			exec(q, "INSERT INTO sessions(id,deployment_id,created_at,ip,ua,snapshot) VALUES(?,?,?,?,?,?)", id, deployment["id"], timestamp(), ip, string(ua), dump(snap))
			saveSessionToken(q, id, token)
			event(q, id, "visit", Doc{"path": r.URL.Path, "method": r.Method})
			session = Doc{"id": id, "snapshot": snap}
		}
		site = entityMaybe(q, "sites_versions", str(object(session["snapshot"])["site_version_id"]))
	})
	return session, site
}

type composerWriter struct {
	http.ResponseWriter
	err   error
	bytes int
}

func (w *composerWriter) Write(b []byte) (int, error) {
	n, e := w.ResponseWriter.Write(b)
	w.bytes += n
	w.err = e
	return n, e
}

func (s *Store) recordComposer(id string, request Doc, result Doc) {
	// Observability failures must not try to send a second HTTP response.
	defer func() {
		if p := recover(); p != nil {
			log.Printf("composer event error: %T", p)
		}
	}()
	s.write(func(q queryer) {
		if count(q, "SELECT COUNT(*) FROM sessions WHERE id=?", id) == 0 {
			return
		}
		event(q, id, "request", Doc{"path": request["path"], "method": request["method"]})
		if boolean(result["rule_matched"]) {
			event(q, id, "rule_matched", Doc{"rule_id": result["rule_id"], "binding_id": result["binding_id"], "trace": result["trace"]})
		}
		event(q, id, "response_sent", Doc{"status": result["status"], "content_type": result["content_type"], "path": request["path"], "method": request["method"], "release_id": result["release_id"], "site_version_id": result["site_version_id"], "site_node_id": request["site_node_id"]})
		if str(request["method"]) != "HEAD" && len(array(result["deliveries"])) > 0 {
			event(q, id, "delivery", Doc{"path": request["path"], "carriers": []any{"scenario"}, "deliveries": result["deliveries"], "body": result["body"], "rule_id": result["rule_id"]})
		}
	})
}

func (a *App) composerPublicRoute(w http.ResponseWriter, r *http.Request, listener Doc) bool {
	config := get(a.store.db, "listeners", str(listener["id"]))
	if str(config["deployment_id"]) == "" {
		return false
	}
	deployment := get(a.store.db, "deployments", str(config["deployment_id"]))
	if deployment["mode"] != "composable" {
		return false
	}
	session, site := a.store.composerSession(r, listener)
	accessSession(r, session)
	snap := object(session["snapshot"])
	var node Doc
	var localPath string
	site, node, localPath = selectWorkspaceSite(a.store.db, snap, r.URL.Path)
	// API probes can reuse X-Run-ID; browsers keep a listener-scoped experiment cookie.
	http.SetCookie(w, &http.Cookie{Name: "am_run_" + str(listener["id"]), Value: str(session["id"]), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	headers := Doc{}
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(413, "测试请求正文不得超过 256 KiB")
	}
	request := Doc{"method": r.Method, "path": r.URL.RequestURI(), "headers": headers, "body": string(body), "site_node_id": node["id"], "site_version_id": node["site_version_id"]}
	rules := []Doc{}
	for _, raw := range array(snap["rules"]) {
		rules = append(rules, object(raw))
	}
	result := evaluateScenarioRules(rules, request, Doc{"run_id": session["id"], "token": snap["token"], "callback_url": snap["callback_url"]})
	if !boolean(result["matched"]) {
		if r.Method == "GET" || r.Method == "HEAD" {
			if asset := hostedSiteFile(site, localPath); asset != nil {
				w.Header().Set("X-Run-ID", str(session["id"]))
				server := str(node["server_header"])
				if server == "" {
					server = str(object(snap["deployment"])["server_header"])
				}
				if server != "" {
					w.Header().Set("Server", server)
				}
				a.serveHostedFile(w, r, asset, session, request)
				return true
			}
			if location := mountedSiteEntryLocation(site, node, r.URL); location != "" {
				merge(result, Doc{"matched": true, "body": "", "status": 307, "content_type": "text/plain; charset=utf-8", "headers": Doc{"Location": location}, "source": "site_redirect"})
			} else if b, mime, found := siteResponse(site, localPath); found {
				merge(result, Doc{"matched": true, "body": b, "status": 200, "content_type": mime, "source": "site"})
			}
		}
		if !boolean(result["matched"]) {
			merge(result, Doc{"body": "Not found", "status": 404, "content_type": "text/plain; charset=utf-8"})
		}
	}
	if str(node["server_header"]) != "" {
		applyWorkspaceServer(result, node)
	} else {
		applyWorkspaceServer(result, object(snap["deployment"]))
	}
	merge(result, Doc{"release_id": snap["release_id"], "site_version_id": node["site_version_id"], "site_node_id": node["id"]})
	responseHeaders := map[string]string{"X-Run-ID": str(session["id"])}
	for key, value := range object(result["headers"]) {
		responseHeaders[key] = str(value)
	}
	writer := &composerWriter{ResponseWriter: w}
	response(writer, r, result["body"], integer(result["status"]), str(result["content_type"]), responseHeaders)
	if writer.err == nil {
		a.store.recordComposer(str(session["id"]), request, result)
		accessComposerResult(r, result)
	}
	return true
}
