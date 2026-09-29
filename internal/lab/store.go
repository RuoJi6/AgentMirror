package lab

import (
	"database/sql"
	"embed"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed assets/*
var resources embed.FS
var defaults = func() Doc { b, err := resources.ReadFile("assets/defaults.json"); check(err); return decode(string(b)) }()

type Store struct {
	hostedRoot string
	db         *sql.DB
	path       string
}

func openStore(path, initialURL string) (store *Store, err error) {
	defer func() {
		if p := recover(); p != nil {
			if store != nil {
				store.db.Close()
			}
			store = nil
			err = caught(p)
		}
	}()
	path, err = filepath.Abs(path)
	check(err)
	check(os.MkdirAll(filepath.Dir(path), 0700))
	// SQLite file URIs require forward slashes and / before a Windows drive.
	uriPath := filepath.ToSlash(path)
	if len(uriPath) >= 2 && uriPath[1] == ':' {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	params := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(15000)", "journal_mode(WAL)"}, "_txlock": {"immediate"}}
	u.RawQuery = params.Encode()
	db, err := sql.Open("sqlite", u.String())
	check(err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store = &Store{db: db, path: path}
	exec(db, `CREATE TABLE IF NOT EXISTS entities(kind TEXT, id TEXT, doc TEXT NOT NULL, PRIMARY KEY(kind,id));
 CREATE TABLE IF NOT EXISTS versions(profile_id TEXT, version INTEGER, doc TEXT NOT NULL, PRIMARY KEY(profile_id,version));
 CREATE TABLE IF NOT EXISTS mcp_servers(id TEXT PRIMARY KEY, doc TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS settings(id INTEGER PRIMARY KEY CHECK(id=1), doc TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY, deployment_id TEXT, created_at TEXT, ip TEXT, ua TEXT, snapshot TEXT, outcome TEXT DEFAULT 'unknown', note TEXT DEFAULT '');
 CREATE TABLE IF NOT EXISTS session_tokens(session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE, token_hash TEXT NOT NULL, issued_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY, session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE, at TEXT, kind TEXT, detail TEXT);
 CREATE TABLE IF NOT EXISTS reports(id INTEGER PRIMARY KEY, session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE, at TEXT, body TEXT);
 CREATE INDEX IF NOT EXISTS event_session ON events(session_id);
 CREATE INDEX IF NOT EXISTS report_session ON reports(session_id);
 CREATE INDEX IF NOT EXISTS session_time ON sessions(created_at);`)
	store.initAccessLog()
	store.write(func(q queryer) {
		if count(q, "SELECT COUNT(*) FROM settings") != 0 {
			return
		}
		exec(q, "INSERT INTO settings VALUES(1,?)", dump(Doc{"public_url": publicURL(initialURL), "default_deployment": nil}))

	})
	store.seedEvaluatedScenarios()
	store.migrateLegacyTemplates()
	store.reconcileSavedMaterials()
	return store, nil
}
func (s *Store) write(fn func(queryer)) {
	tx, err := s.db.Begin()
	check(err)
	defer tx.Rollback()
	fn(tx)
	check(tx.Commit())
}
func get(q queryer, kind, id string) Doc {
	var raw string
	err := q.QueryRow("SELECT doc FROM entities WHERE kind=? AND id=?", kind, id).Scan(&raw)
	if err == sql.ErrNoRows {
		fail(404, "记录不存在")
	}
	check(err)
	return decode(raw)
}
func listing(q queryer, kind string) []Doc {
	result := []Doc{}
	for _, row := range rows(q, "SELECT doc FROM entities WHERE kind=? ORDER BY rowid", kind) {
		result = append(result, decode(str(row["doc"])))
	}
	return result
}
func settings(q queryer) Doc {
	var raw string
	check(q.QueryRow("SELECT doc FROM settings WHERE id=1").Scan(&raw))
	return decode(raw)
}
func put(q queryer, kind string, doc Doc) {
	exec(q, "INSERT INTO entities VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET doc=excluded.doc", kind, doc["id"], dump(doc))
}
func putSettings(q queryer, doc Doc) { exec(q, "UPDATE settings SET doc=? WHERE id=1", dump(doc)) }

// templates/deployments branches are compatibility helpers for imported legacy
// data and internal fixtures, never exposed through the management router.
func (s *Store) save(kind string, raw Doc) Doc {
	return s.saveWithProfileOrigin(kind, raw, Doc{"kind": "manual"})
}

// origin comes only from the caller's execution context. Never accept it from
// raw: editing or restoring an Agent-authored version is a new manual save.
func (s *Store) saveWithProfileOrigin(kind string, raw Doc, origin Doc) Doc {
	if kind != "templates" && kind != "profiles" && kind != "deployments" {
		fail(404, "未知资源")
	}
	id := str(raw["id"])
	if raw["id"] == nil || raw["id"] == "" {
		id = randomHex(6)
	}
	if !idPattern.MatchString(id) {
		fail(400, "记录编号格式不正确")
	}
	doc := Doc{"id": id, "name": textField(raw, "name", 80, true)}
	s.write(func(q queryer) {
		var old Doc
		if str(raw["id"]) != "" {
			old = get(q, kind, id)
		}
		if kind == "deployments" && old["mode"] == "composable" {
			fail(400, "请在蜜罐工作区修改并发布此部署")
		}
		doc["created_at"] = timestamp()
		if old != nil {
			doc["created_at"] = old["created_at"]
		}
		doc["updated_at"] = timestamp()
		switch kind {
		case "templates":
			templateKind := textField(raw, "kind", 200, true)
			if templateKind != "login" && templateKind != "docs" && templateKind != "repository" && templateKind != "files" {
				fail(400, "未知页面类型")
			}
			if templateKind != "login" {
				for _, d := range listing(q, "deployments") {
					if str(d["template_id"]) == id && d["trigger"] == "after_login" {
						fail(400, "此模板被登录后投放的部署使用，不能修改为其他页面类型")
					}
				}
				for _, l := range listing(q, "listeners") {
					if object(l["overrides"])["trigger"] == "after_login" && get(q, "deployments", str(l["deployment_id"]))["template_id"] == id {
						fail(400, "此模板被端口的登录后投放配置使用，不能修改页面类型")
					}
				}
			}
			doc["kind"] = templateKind
			merge(doc, templateConfig(raw, old))
		case "profiles":
			if old != nil && integer(raw["version"]) != integer(old["version"]) {
				fail(409, "方案已被修改，请刷新后重试，避免覆盖别人的编辑")
			}
			// Keep legacy metadata when editing an old profile, without requiring
			// level or rewriting its historical versions during the upgrade.
			doc["level"] = textField(raw, "level", 40, false)
			if _, exists := raw["level"]; !exists && old != nil {
				doc["level"] = old["level"]
			}
			category := str(optional(raw, "category", optional(old, "category", "command_execution")))
			if category != "command_execution" && category != "ethical_blocking" && category != "unexpected_output" {
				fail(400, "未知方案分组，请选择执行命令、道德阻断或意外输出")
			}
			doc["category"] = category
			doc["description"] = textField(raw, "description", 500, false)
			doc["body"] = textField(raw, "body", 50000, true)

			doc["fields"], doc["commands"] = validateSpec(raw)
			doc["version"] = integer(old["version"]) + 1
			doc["revision_origin"] = clone(origin)
			exec(q, "INSERT INTO versions VALUES(?,?,?)", id, doc["version"], dump(doc))
		case "deployments":
			doc["slug"] = textField(raw, "slug", 60, true)
			if !slugPattern.MatchString(str(doc["slug"])) {
				fail(400, "路径仅支持小写字母、数字和连字符")
			}
			for _, d := range listing(q, kind) {
				if d["slug"] == doc["slug"] && d["id"] != id {
					fail(409, "此部署路径已被使用")
				}
			}
			doc["template_id"] = get(q, "templates", textField(raw, "template_id", 200, true))["id"]
			merge(doc, deliveryConfig(q, raw, str(doc["template_id"])))
			enabled, ok := raw["enabled"].(bool)
			if !ok {
				fail(400, "enabled 必须是布尔值")
			}
			doc["enabled"] = enabled
			for _, l := range listing(q, "listeners") {
				if l["deployment_id"] == id {
					effectiveDeployment(q, l, doc)
				}
			}
		}
		put(q, kind, doc)
	})
	return doc
}
func surfaceConfig(kind string) Doc {
	for _, raw := range array(defaults["surfaces"]) {
		d := object(raw)
		if d["kind"] == kind {
			return d
		}
	}
	return nil
}
func deliveryConfig(q queryer, raw Doc, templateID string) Doc {
	profileID := get(q, "profiles", textField(raw, "profile_id", 200, true))["id"]
	carriers, ok := raw["carriers"].([]any)
	if !ok || len(carriers) == 0 {
		fail(400, "至少选择一个有效投放载体")
	}
	unique := []any{}
	seen := map[string]bool{}
	for _, c := range carriers {
		name, ok := c.(string)
		if !ok || object(defaults["carriers"])[name] == nil {
			fail(400, "至少选择一个有效投放载体")
		}
		if !seen[name] {
			unique = append(unique, name)
			seen[name] = true
		}
	}
	surfaces, ok := optional(raw, "surfaces", []any{}).([]any)
	if !ok || len(surfaces) > len(array(defaults["surfaces"])) {
		fail(400, "暴露面配置无效")
	}
	result := []any{}
	seen = map[string]bool{}
	for _, value := range surfaces {
		surface := object(value)
		kind := str(surface["kind"])
		if surface == nil || surfaceConfig(kind) == nil || seen[kind] {
			fail(400, "暴露面类型无效或重复")
		}
		seen[kind] = true
		sid := str(surface["profile_id"])
		if sid == "" {
			sid = str(profileID)
		}
		get(q, "profiles", sid)
		result = append(result, Doc{"kind": kind, "profile_id": sid, "enabled": true})
	}
	trigger := str(optional(raw, "trigger", "page_load"))
	if trigger != "page_load" && trigger != "after_login" {
		fail(400, "未知投放时机")
	}
	if trigger == "after_login" && get(q, "templates", templateID)["kind"] != "login" {
		fail(400, "登录后投放仅适用于管理登录页面")
	}
	return Doc{"profile_id": profileID, "carriers": unique, "surfaces": result, "trigger": trigger, "linked_surfaces": boolean(optional(raw, "linked_surfaces", true))}
}
func effectiveDeployment(q queryer, listener, deployment Doc) Doc {
	if deployment == nil {
		deployment = get(q, "deployments", str(listener["deployment_id"]))
	}
	result := clone(deployment)
	if result["mode"] == "composable" {
		if listener["overrides"] != nil || str(fallback(listener["root_surface"], "page")) != "page" {
			fail(400, "组合部署的页面与场景请在蜜罐工作区配置，端口使用继承部署")
		}
		return result
	}
	if listener["overrides"] != nil {
		merge(result, deliveryConfig(q, object(listener["overrides"]), str(result["template_id"])))
	}
	root := str(fallback(listener["root_surface"], "page"))
	found := root == "page"
	for _, surface := range array(result["surfaces"]) {
		if object(surface)["kind"] == root {
			found = true
		}
	}
	if !found {
		fail(400, "端口首页对应的暴露面必须启用，请先调整端口配置")
	}
	return result
}
func prepareListener(q queryer, raw Doc, adminPort int) Doc {
	id := str(raw["id"])
	if id == "admin" || raw["role"] == "admin" {
		fail(400, "管理后台仅通过启动参数配置")
	}
	if raw["id"] == nil || raw["id"] == "" {
		id = randomHex(6)
	}
	if !idPattern.MatchString(id) {
		fail(400, "端口编号格式不正确")
	}
	var old Doc
	if str(raw["id"]) != "" {
		old = get(q, "listeners", id)
	}
	host := textField(raw, "host", 64, true)
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || strings.Contains(host, ":") {
		fail(400, "监听地址需为 IPv4 地址，例如 0.0.0.0 或 127.0.0.1")
	}
	p, ok := number(raw["port"])
	if !ok || p < 1 || p > 65535 {
		fail(400, "端口必须是 1–65535 的整数")
	}
	port := int(p)
	if port == adminPort {
		fail(409, "此端口已被管理后台占用，请选择其他测试端口")
	}
	for _, l := range listing(q, "listeners") {
		if integer(l["port"]) == port && l["id"] != id {
			fail(409, "此端口已被其他测试端口配置占用")
		}
	}
	address := publicURL(textField(raw, "public_url", 500, true))
	u, _ := url.Parse(address)
	portText := u.Port()
	if portText == "" {
		portText = "80"
	}
	if u.Scheme != "http" || portText != fmtPort(port) || u.Hostname() == "0.0.0.0" {
		fail(400, "公告地址需使用可访问的主机 IP / 域名、http 协议和当前监听端口")
	}
	enabled, ok := raw["enabled"].(bool)
	if !ok {
		fail(400, "enabled 必须是布尔值")
	}
	role := str(fallback(old["role"], "public"))
	if fallback(raw["role"], role) != role {
		fail(400, "端口用途不能修改")
	}
	doc := Doc{"id": id, "role": role, "name": textField(raw, "name", 80, true), "host": host, "port": port, "public_url": address, "enabled": enabled, "created_at": fallback(old["created_at"], timestamp()), "updated_at": timestamp()}
	if str(raw["deployment_id"]) == "" {
		if enabled {
			fail(400, "请先创建并绑定部署，再启用测试端口")
		}
		return merge(doc, Doc{"deployment_id": nil, "root_surface": "page", "enabled": false, "overrides": nil})
	}
	deployment := get(q, "deployments", textField(raw, "deployment_id", 200, true))
	doc["deployment_id"] = deployment["id"]
	nodeID := textField(raw, "site_node_id", 64, false)
	if _, supplied := raw["site_node_id"]; !supplied && old["deployment_id"] == deployment["id"] {
		nodeID = listenerSiteID(old)
	}
	if nodeID == "" {
		nodeID = workspaceRootSite
	}
	if nodeID != workspaceRootSite {
		if deployment["mode"] != "composable" {
			fail(400, "子站点端口必须绑定蜜罐工作区")
		}
		doc["site_node_id"] = nodeID
		workspace := get(q, "workspaces", str(deployment["workspace_id"]))
		validateListenerSite(q, workspace, doc, compileWorkspaceBindings(q, workspace))
		listenerSiteRelease(get(q, "composer_releases", str(deployment["release_id"])), doc)
	}
	root := str(fallback(raw["root_surface"], "page"))
	if root != "page" && surfaceConfig(root) == nil {
		fail(400, "端口首页类型无效")
	}
	doc["root_surface"] = root
	if raw["overrides"] != nil && object(raw["overrides"]) == nil {
		fail(400, "独立投放配置无效")
	}
	doc["overrides"] = raw["overrides"]
	if boolean(old["legacy_paths"]) {
		doc["legacy_paths"] = true
	}
	effective := effectiveDeployment(q, doc, nil)
	if doc["overrides"] != nil {
		doc["overrides"] = deliveryConfig(q, object(doc["overrides"]), str(effective["template_id"]))
	}
	if enabled && !boolean(effective["enabled"]) {
		fail(400, "请先启用绑定的部署")
	}
	return doc
}
func (s *Store) delete(kind, id string, expected ...int) {
	s.write(func(q queryer) {
		current := get(q, kind, id)
		if len(expected) > 0 && integer(current["version"]) != expected[0] {
			fail(409, "素材已更新，请重新读取后删除")
		}
		switch kind {
		case "deployments":
			set := settings(q)
			if set["default_deployment"] == id && (!boolean(set["ports_unified"]) || str(set["default_listener"]) != "") {
				fail(400, "请先将其他部署设为默认首页")
			}
			for _, l := range listing(q, "listeners") {
				if l["deployment_id"] == id {
					fail(400, "此部署被端口引用，请先修改或删除端口绑定")
				}
			}
		case "profiles", "templates":
			if kind == "profiles" {
				for _, workspace := range listing(q, "workspaces") {
					for _, binding := range array(workspace["bindings"]) {
						if object(binding)["profile_id"] == id {
							fail(400, "此提示词被蜜罐工作区引用，请先修改实例绑定")
						}
						for _, profileID := range object(object(binding)["rule_profiles"]) {
							if profileID == id {
								fail(400, "此提示词被蜜罐工作区接口引用，请先修改接口绑定")
							}
						}
					}
				}
			}
			key := "profile_id"
			if kind == "templates" {
				key = "template_id"
			}
			for _, d := range listing(q, "deployments") {
				if kind == "profiles" && d["mode"] != "composable" {
					if !legacyDeploymentReferenced(q, d) {
						// Archived legacy deployments have no authoring API and cannot
						// acquire new listeners. History already contains the profile.
						continue
					}
				}
				if d[key] == id {
					fail(400, "此配置被部署引用，请先修改或删除对应部署")
				}
				if kind == "profiles" {
					for _, v := range array(d["surfaces"]) {
						if object(v)["profile_id"] == id {
							fail(400, "此配置被部署引用，请先修改或删除对应部署")
						}
					}
				}
			}
			if kind == "profiles" {
				for _, l := range listing(q, "listeners") {
					o := object(l["overrides"])
					used := o["profile_id"] == id
					for _, v := range array(o["surfaces"]) {
						used = used || object(v)["profile_id"] == id
					}
					if used {
						fail(400, "此提示词被端口的独立配置引用，请先修改端口绑定")
					}
				}
			}
		default:
			fail(404, "未知资源")
		}
		exec(q, "DELETE FROM entities WHERE kind=? AND id=?", kind, id)
		if kind == "profiles" {
			exec(q, "DELETE FROM versions WHERE profile_id=?", id)
		}
	})
}
func (s *Store) versions(id string) []Doc {
	get(s.db, "profiles", id)
	result := []Doc{}
	for _, row := range rows(s.db, "SELECT doc FROM versions WHERE profile_id=? ORDER BY version DESC", id) {
		result = append(result, decode(str(row["doc"])))
	}
	s.resolveLegacyProfileOrigins(id, result)
	return result
}
