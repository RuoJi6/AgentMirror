package lab

import (
	"strings"
)

func validVariable(key string) bool {
	return key == "run_id" || key == "token" || key == "callback_url" || key == "fields" || key == "commands"
}
func validateSpec(raw Doc) ([]any, []any) {
	fields, ok := optional(raw, "fields", []any{}).([]any)
	commands, ok2 := optional(raw, "commands", []any{}).([]any)
	if !ok || !ok2 || len(fields) > 40 || len(commands) > 30 {
		fail(400, "字段最多 40 项，命令最多 30 项")
	}
	fieldResult, commandResult := []any{}, []any{}
	seen := map[string]bool{}
	for _, value := range fields {
		item := object(value)
		if item == nil {
			fail(400, "字段格式不正确")
		}
		name := textField(item, "name", 64, true)
		if !fieldPattern.MatchString(name) || seen[name] {
			fail(400, "字段名需唯一，仅使用字母、数字、下划线，且不能以数字开头")
		}
		seen[name] = true
		kind := str(item["type"])
		required, ok := item["required"].(bool)
		if !ok || (kind != "string" && kind != "number" && kind != "boolean" && kind != "object" && kind != "array") {
			fail(400, "字段类型或必填设置不正确")
		}
		fieldResult = append(fieldResult, Doc{"name": name, "type": kind, "required": required, "description": textField(item, "description", 500, false)})
	}
	seen = map[string]bool{}
	for _, value := range commands {
		item := object(value)
		if item == nil {
			fail(400, "命令格式不正确")
		}
		id := textField(item, "id", 64, true)
		if !idPattern.MatchString(id) || seen[id] {
			fail(400, "命令编号需唯一，仅使用字母、数字、下划线或连字符")
		}
		seen[id] = true
		commandResult = append(commandResult, Doc{"id": id, "label": textField(item, "label", 100, true), "command": textField(item, "command", 10000, true), "expected": textField(item, "expected", 500, false)})
	}
	return fieldResult, commandResult
}
func renderInstruction(profile Doc, values Doc) string {
	context := clone(values)
	context["fields"] = pretty(profile["fields"])
	context["commands"] = pretty(profile["commands"])
	return variablePattern.ReplaceAllStringFunc(str(profile["body"]), func(token string) string {
		match := variablePattern.FindStringSubmatch(token)
		key := strings.TrimSpace(match[1])
		if !validVariable(key) {
			return token
		}
		return str(context[key])
	})
}
func event(q queryer, id, kind string, detail Doc) {
	exec(q, "INSERT INTO events(session_id,at,kind,detail) VALUES(?,?,?,?)", id, timestamp(), kind, dump(detail))
}
func (s *Store) newSession(slug, ip, ua, path, surfaceKind string, listener Doc) Doc {
	var result Doc
	s.write(func(q queryer) {
		config := get(q, "listeners", str(listener["id"]))
		if !boolean(config["enabled"]) || integer(config["port"]) != integer(listener["port"]) {
			fail(404, "端口已停用")
		}
		deployment := effectiveDeployment(q, config, nil)
		if slug != "" && slug != deployment["slug"] {
			if !boolean(config["legacy_paths"]) {
				fail(404, "此部署未绑定到当前端口")
			}
			deployment = nil
			for _, d := range listing(q, "deployments") {
				if d["slug"] == slug {
					deployment = d
					break
				}
			}
		}
		if deployment == nil || !boolean(deployment["enabled"]) {
			fail(404, "页面不存在或暂不可用")
		}
		var surface Doc
		for _, value := range array(deployment["surfaces"]) {
			d := object(value)
			if surfaceKind != "" && d["kind"] == surfaceKind && boolean(fallback(d["enabled"], true)) {
				surface = d
				break
			}
		}
		if surfaceKind != "" && surface == nil {
			fail(404, "暴露面未启用")
		}
		profileID := str(deployment["profile_id"])
		if surface != nil {
			profileID = str(surface["profile_id"])
		}
		profile := get(q, "profiles", profileID)
		id, token := randomHex(8), randomToken(24)
		callback := listenerCallbackURL(config, deployment)
		bound := Doc{}
		for _, key := range []string{"id", "name", "host", "port", "public_url"} {
			bound[key] = config[key]
		}
		snapshot := Doc{"deployment": deployment, "profile": profile, "template": get(q, "templates", str(deployment["template_id"])), "token": token, "callback_url": callback, "surface": surface, "listener": bound}
		snapshot["instruction"] = renderInstruction(profile, Doc{"run_id": id, "token": token, "callback_url": callback})
		uaRunes := []rune(ua)
		if len(uaRunes) > 1000 {
			ua = string(uaRunes[:1000])
		}
		exec(q, "INSERT INTO sessions(id,deployment_id,created_at,ip,ua,snapshot) VALUES(?,?,?,?,?,?)", id, deployment["id"], timestamp(), ip, ua, dump(snapshot))
		saveSessionToken(q, id, token)
		event(q, id, "visit", Doc{"path": path})
		result = Doc{"id": id, "snapshot": snapshot}
	})
	return result
}
func (s *Store) session(id string) Doc { return s.sessionView(id, 0) }

func (s *Store) sessionView(id string, reportPage int) Doc {
	// Read the row and children from one database snapshot, including during clear.
	var result Doc
	s.write(func(q queryer) {
		found := rows(q, "SELECT * FROM sessions WHERE id=?", id)
		if len(found) == 0 {
			fail(404, "会话不存在")
		}
		result = found[0]
		result["snapshot"] = decode(str(result["snapshot"]))
		if reportPage > 0 {
			// Live UI metadata is deliberately excluded from historical exports.
			result["current_profiles"] = currentPromptVersions(q, object(result["snapshot"]))
			snap := object(result["snapshot"])
			listener := entityMaybe(q, "listeners", str(object(snap["listener"])["id"]))
			deployment := entityMaybe(q, "deployments", str(object(snap["deployment"])["id"]))
			if listener != nil && deployment != nil && listener["deployment_id"] == deployment["id"] {
				result["current_callback_url"] = listenerCallbackURL(listener, deployment)
			}
			if reportPage > 100000000 {
				fail(400, "页码无效")
			}
			result["report_count"] = count(q, "SELECT COUNT(*) FROM reports WHERE session_id=?", id)
			result["event_count"] = count(q, "SELECT COUNT(*) FROM events WHERE session_id=?", id)
			result["delivered"] = count(q, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", id) > 0
			result["reports_page"] = reportPage
			result["reports_page_size"] = 5
			// Never load complete receipt bodies into the list response.
			result["reports"] = rows(q, "SELECT id,at,substr(body,1,600) AS preview,length(body) AS characters,length(CAST(body AS BLOB)) AS bytes FROM reports WHERE session_id=? ORDER BY id DESC LIMIT 5 OFFSET ?", id, (reportPage-1)*5)
			events := rows(q, "SELECT * FROM events WHERE session_id=? ORDER BY id DESC LIMIT 100", id)
			for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
				events[i], events[j] = events[j], events[i]
			}
			for _, e := range events {
				e["detail"] = decode(str(e["detail"]))
			}
			result["events"] = events
		} else {
			events := rows(q, "SELECT * FROM events WHERE session_id=? ORDER BY id", id)
			for _, e := range events {
				e["detail"] = decode(str(e["detail"]))
			}
			result["events"] = events
			reports := rows(q, "SELECT * FROM reports WHERE session_id=? ORDER BY id", id)
			for _, r := range reports {
				r["body"] = decode(str(r["body"]))
			}
			result["reports"] = reports
		}
	})
	return result
}
func (s *Store) sessions(search, status, deployment string, page int) Doc {
	conditions := []string{"1=1"}
	params := []any{}
	if search != "" {
		conditions = append(conditions, "(s.id LIKE ? OR s.ip LIKE ? OR s.ua LIKE ?)")
		for i := 0; i < 3; i++ {
			params = append(params, "%"+search+"%")
		}
	}
	if deployment != "" {
		conditions = append(conditions, "s.deployment_id=?")
		params = append(params, deployment)
	}
	switch status {
	case "received":
		conditions = append(conditions, "EXISTS(SELECT 1 FROM reports r WHERE r.session_id=s.id)")
	case "delivered":
		conditions = append(conditions, "EXISTS(SELECT 1 FROM events e WHERE e.session_id=s.id AND e.kind='delivery') AND NOT EXISTS(SELECT 1 FROM reports r WHERE r.session_id=s.id)")
	case "visited":
		conditions = append(conditions, "NOT EXISTS(SELECT 1 FROM events e WHERE e.session_id=s.id AND e.kind='delivery')")
	case "refused":
		conditions = append(conditions, "s.outcome='refused'")
	}
	query := " FROM sessions s WHERE " + strings.Join(conditions, " AND ")
	if page < 1 {
		page = 1
	}
	if page > 100000000 {
		fail(400, "页码无效")
	}
	var result Doc
	s.write(func(q queryer) {
		total := count(q, "SELECT COUNT(*)"+query, params...)
		args := append(append([]any{}, params...), (page-1)*20)
		items := rows(q, "SELECT s.*, (SELECT COUNT(*) FROM reports r WHERE r.session_id=s.id) report_count, (SELECT COUNT(*) FROM events e WHERE e.session_id=s.id AND e.kind='delivery') delivery_count"+query+" ORDER BY created_at DESC, s.rowid DESC LIMIT 20 OFFSET ?", args...)
		for _, item := range items {
			snap := decode(str(item["snapshot"]))
			delete(item, "snapshot")
			d, p := object(snap["deployment"]), object(snap["profile"])
			carriers := d["carriers"]
			if surface := object(snap["surface"]); surface != nil {
				carriers = []any{surface["kind"]}
			}
			merge(item, Doc{"deployment_name": d["name"], "profile_name": p["name"], "version": p["version"], "carriers": carriers, "surface": snap["surface"], "listener": snap["listener"]})
			if d["mode"] == "composable" {
				profiles := []any{}
				seen := map[string]bool{}
				for _, raw := range array(snap["rules"]) {
					profile := object(object(raw)["profile"])
					if profile != nil {
						key := str(profile["id"]) + ":" + dump(profile["version"])
						if !seen[key] {
							profiles = append(profiles, Doc{"id": profile["id"], "name": profile["name"], "version": profile["version"]})
							seen[key] = true
						}
					}
				}
				merge(item, Doc{"mode": "composable", "profiles": profiles, "carriers": []any{"scenario"}})
			}
		}
		result = Doc{"items": items, "total": total, "page": page, "page_size": 20}
	})
	return result
}
func checkListener(snapshot, listener Doc) {
	if listener == nil {
		return
	}
	bound := object(snapshot["listener"])
	if bound == nil && listener["id"] == "primary" {
		return
	}
	if bound == nil || bound["id"] != listener["id"] || integer(bound["port"]) != integer(listener["port"]) || listenerSiteID(bound) != listenerSiteID(listener) {
		fail(403, "会话不属于当前端口")
	}
}
func (s *Store) collect(body, listener Doc) Doc {
	result, rejected := s.collectAttempt(body, listener)
	if rejected {
		fail(403, "实验编号或令牌无效")
	}
	if result["ok"] == false {
		fail(422, "回传字段不符合当前工作区要求")
	}
	return result
}

// Keep authentication and receipt storage in one transaction. A custom HTTP
// rejection response never changes whether the submitted data is accepted.
func (s *Store) collectAttempt(body, listener Doc) (Doc, bool) {
	id, ok := body["run_id"].(string)
	token, ok2 := body["token"].(string)
	if !ok || !ok2 {
		fail(400, "缺少 run_id 或 token")
	}
	var result Doc
	rejected := false
	s.write(func(q queryer) {
		found := rows(q, "SELECT snapshot FROM sessions WHERE id=?", id)
		if len(found) == 0 {
			rejected = true
			return
		}
		snap := decode(str(found[0]["snapshot"]))
		checkListener(snap, listener)
		if !validSessionToken(q, id, snap, token) {
			rejected = true
			return
		}
		// Apply the owning workspace's current published policy to existing
		// sessions too. Never confuse schema validity with command execution.
		deployment := object(snap["deployment"])
		if current := entityMaybe(q, "deployments", str(deployment["id"])); current != nil {
			deployment = current
		}
		validation := validateCollectedData(collectValidationConfig(deployment), body)
		if validation != nil && validation["schema"] == "failed" {
			event(q, id, "receipt_rejected", Doc{"reason": "invalid_data", "validation": validation})
			result = Doc{"ok": false, "error": "invalid_data", "validation": validation}
			return
		}
		// Preserve all values exactly as received after optional schema checks,
		// excluding the authentication envelope. Legacy workspaces remain freeform.
		content := Doc{}
		for key, value := range body {
			if key != "run_id" && key != "token" {
				content[key] = value
			}
		}
		if len(content) == 0 {
			fail(400, "缺少回传内容")
		}
		r := exec(q, "INSERT INTO reports(session_id,at,body) VALUES(?,?,?)", id, timestamp(), dump(content))
		receipt, err := r.LastInsertId()
		check(err)
		result = Doc{"ok": true, "receipt_id": receipt}
		detail := Doc{"report_id": receipt}
		if validation != nil {
			result["validation"], detail["validation"] = validation, validation
		}
		event(q, id, "receipt", detail)
	})
	return result, rejected
}
func (s *Store) label(id string, body Doc) {
	outcome := str(body["outcome"])
	if outcome != "unknown" && outcome != "refused" && outcome != "executed" {
		fail(400, "未知结论")
	}
	note := textField(body, "note", 2000, false)
	s.write(func(q queryer) {
		result := exec(q, "UPDATE sessions SET outcome=?,note=? WHERE id=?", outcome, note, id)
		n, err := result.RowsAffected()
		check(err)
		if n == 0 {
			fail(404, "会话不存在")
		}
		event(q, id, "annotation", Doc{"outcome": outcome, "note": note})
	})
}
func (s *Store) clear() Doc {
	result := Doc{}
	s.write(func(q queryer) {
		result["cleared"] = count(q, "SELECT COUNT(*) FROM sessions")
		exec(q, "DELETE FROM access_requests")
		exec(q, "DELETE FROM sessions")
	})
	return result
}
func (s *Store) stats() Doc {
	return Doc{"sessions": count(s.db, "SELECT COUNT(*) FROM sessions"), "delivered": count(s.db, "SELECT COUNT(DISTINCT session_id) FROM events WHERE kind='delivery'"), "reports": count(s.db, "SELECT COUNT(*) FROM reports")}
}
func (s *Store) export() Doc {
	items := []Doc{}
	// Each session has a consistent row/children read; keep export bounded to an
	// immutable list of identifiers, like the original API.
	for _, r := range rows(s.db, "SELECT id FROM sessions ORDER BY created_at DESC") {
		items = append(items, s.session(str(r["id"])))
	}
	return Doc{"exported_at": timestamp(), "sessions": items}
}

// Return one raw receipt on demand; a large or deeply nested JSON value is
// transported as text so the browser can render a bounded window safely.
func (s *Store) reportContent(sessionID, reportID string) Doc {
	found := rows(s.db, "SELECT id,at,body AS content FROM reports WHERE session_id=? AND CAST(id AS TEXT)=?", sessionID, reportID)
	if len(found) == 0 {
		fail(404, "回传不存在")
	}
	return found[0]
}
