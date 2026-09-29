package lab

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// MCP connection credentials are deliberately outside entities, workspace
// exports and generation jobs. Public documents expose keys, never values.
func mcpServer(q queryer, id string) Doc {
	var raw string
	err := q.QueryRow("SELECT doc FROM mcp_servers WHERE id=?", id).Scan(&raw)
	if err == sql.ErrNoRows {
		fail(404, "MCP 服务不存在")
	}
	check(err)
	return decode(raw)
}

func publicMCP(d Doc) Doc {
	d = clone(d)
	for _, key := range []string{"headers", "env"} {
		masked := Doc{}
		for k := range object(d[key]) {
			masked[k] = nil
		}
		d[key] = masked
	}
	return d
}

var mcpKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var mcpHeaderPattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func mcpSecretMap(raw, old any, headers bool) Doc {
	if raw == nil {
		return object(old)
	}
	d := object(raw)
	if d == nil || len(d) > 64 {
		fail(400, "环境变量或请求头必须为对象，最多 64 项")
	}
	out := Doc{}
	for k, v := range d {
		pattern := mcpKeyPattern
		if headers {
			pattern = mcpHeaderPattern
		}
		if !pattern.MatchString(k) || len(k) > 128 {
			fail(400, "环境变量或请求头名称无效")
		}
		if headers {
			switch strings.ToLower(k) {
			case "host", "content-length", "connection", "transfer-encoding", "accept", "content-type", "mcp-session-id", "mcp-protocol-version":
				fail(400, "不能覆盖 MCP 协议请求头："+k)
			}
		}
		if v == nil {
			if previous, ok := object(old)[k]; ok {
				out[k] = previous
				continue
			}
			fail(400, "新建的环境变量或请求头需要填写值")
		}
		s, ok := v.(string)
		if !ok || len(s) > 8192 || strings.ContainsRune(s, 0) || (headers && strings.ContainsAny(s, "\r\n")) {
			fail(400, "环境变量或请求头的值无效")
		}
		out[k] = s
	}
	return out
}

func (s *Store) saveMCP(raw Doc) Doc {
	var result Doc
	s.write(func(q queryer) {
		id := textField(raw, "id", 64, false)
		old := Doc{}
		if id == "" {
			id = randomHex(6)
		} else {
			old = mcpServer(q, id)
			if integer(raw["version"]) != integer(old["version"]) {
				fail(409, "MCP 配置已更新，请重新打开")
			}
		}
		transport := textField(raw, "transport", 20, true)
		if !has([]any{"http", "sse", "stdio"}, transport) {
			fail(400, "请选择 HTTP、SSE 或 stdio")
		}
		enabled, ok := raw["enabled"].(bool)
		if !ok {
			fail(400, "enabled 必须为布尔值")
		}
		timeout, ok := number(optional(raw, "timeout_seconds", 30))
		if !ok || timeout < 1 || timeout > 300 {
			fail(400, "MCP 超时时间需为 1–300 秒")
		}
		result = Doc{"id": id, "name": textField(raw, "name", 80, true), "transport": transport, "enabled": enabled, "timeout_seconds": timeout, "version": integer(old["version"]) + 1, "updated_at": timestamp(), "url": "", "command": "", "args": []any{}, "headers": Doc{}, "env": Doc{}, "tools": []any{}, "status": "unchecked", "error": ""}
		if transport == "stdio" {
			result["command"] = textField(raw, "command", 2048, true)
			args := array(raw["args"])
			if args == nil || len(args) > 64 {
				fail(400, "命令参数必须为数组，最多 64 项")
			}
			for _, a := range args {
				s, ok := a.(string)
				if !ok || len(s) > 8192 || strings.ContainsRune(s, 0) {
					fail(400, "命令参数必须为有效字符串")
				}
			}
			result["args"] = args
			result["env"] = mcpSecretMap(raw["env"], old["env"], false)
		} else {
			endpoint := textField(raw, "url", 4096, true)
			u, err := url.Parse(endpoint)
			if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
				fail(400, "请输入有效 HTTP(S) MCP 地址；认证信息请放在请求头中")
			}
			result["url"] = endpoint
			result["headers"] = mcpSecretMap(raw["headers"], old["headers"], true)
		}
		// Name, timeout or enable changes do not invalidate the schema cache.
		unchanged := true
		for _, key := range []string{"transport", "url", "command", "args", "headers", "env"} {
			if string(jsonBytes(result[key])) != string(jsonBytes(old[key])) {
				unchanged = false
			}
		}
		if unchanged {
			for _, key := range []string{"tools", "status", "error", "checked_at"} {
				result[key] = old[key]
			}
		}
		exec(q, "INSERT INTO mcp_servers(id,doc) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET doc=excluded.doc", id, dump(result))
	})
	return publicMCP(result)
}

func (s *Store) cacheMCP(config Doc, tools []any, err error) Doc {
	var result Doc
	s.write(func(q queryer) {
		result = mcpServer(q, str(config["id"]))
		if result["version"] != config["version"] {
			fail(409, "连接期间配置已更新，请重新测试")
		}
		result["checked_at"] = timestamp()
		result["tools"], result["status"], result["error"] = tools, "ready", ""
		if err != nil {
			result["tools"], result["status"], result["error"] = []any{}, "error", safeMCPError(config, err)
		}
		exec(q, "UPDATE mcp_servers SET doc=? WHERE id=?", dump(result), result["id"])
	})
	return publicMCP(result)
}

func (s *Store) deleteMCP(id string, version int) {
	s.write(func(q queryer) {
		d := mcpServer(q, id)
		if version != integer(d["version"]) {
			fail(409, "MCP 配置已更新，请重新打开")
		}
		// Remove bindings atomically, so subsequent Agent edits cannot resurrect
		// a dangling permission. Running jobs keep their private snapshots.
		for _, agent := range listing(q, "agent_definitions") {
			if !has(array(agent["mcp_servers"]), id) {
				continue
			}
			ids := []any{}
			for _, v := range array(agent["mcp_servers"]) {
				if v != id {
					ids = append(ids, v)
				}
			}
			agent["mcp_servers"], agent["version"], agent["updated_at"] = ids, integer(agent["version"])+1, timestamp()
			put(q, "agent_definitions", agent)
		}
		exec(q, "DELETE FROM mcp_servers WHERE id=?", id)
	})
}

func (a *App) mcpRoute(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/mcp" && !strings.HasPrefix(r.URL.Path, "/api/mcp/") {
		return false
	}
	a.generationManager()
	send := func(v any) { response(w, r, v, 200, "", nil) }
	if r.URL.Path == "/api/mcp" {
		switch r.Method {
		case "GET":
			items := []any{}
			for _, row := range rows(a.store.db, "SELECT doc FROM mcp_servers ORDER BY rowid") {
				items = append(items, publicMCP(decode(str(row["doc"]))))
			}
			send(Doc{"items": items})
			return true
		case "POST":
			send(a.store.saveMCP(generationBody(w, r)))
			return true
		}
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/mcp/"), "/")
	if len(parts) == 2 && r.Method == "POST" {
		config := mcpServer(a.store.db, parts[0])
		body := generationBody(w, r)
		if integer(body["version"]) != integer(config["version"]) {
			fail(409, "MCP 配置已更新，请重新打开")
		}
		switch parts[1] {
		case "test":
			ctx, cancel := context.WithTimeout(r.Context(), mcpTimeout(config))
			defer cancel()
			conn, err := connectMCP(ctx, config)
			var tools []any
			if err == nil {
				defer conn.Close()
				tools, err = discoverMCP(ctx, conn)
			}
			send(a.store.cacheMCP(config, tools, err))
			return true
		case "delete":
			a.store.deleteMCP(parts[0], integer(body["version"]))
			send(Doc{"ok": true})
			return true
		}
	}
	fail(405, "请求方法不支持")
	return true
}
