package lab

import "strings"

// Empty values deliberately omit the header; never invent a server fingerprint.
func workspaceServerHeader(doc Doc) string { return str(doc["server_header"]) }

func validateServerHeader(raw Doc) string {
	value := textField(raw, "server_header", 200, false)
	// Validate raw bytes too: TrimSpace must not conceal CR/LF or control bytes.
	for _, r := range str(raw["server_header"]) {
		if r < 0x20 || r > 0x7e {
			fail(400, "Server 响应头仅支持可打印 ASCII 字符，不能包含换行或控制字符")
		}
	}
	if strings.Contains(value, "{{") || strings.Contains(value, "}}") {
		fail(400, "Server 响应头不支持模板槽位")
	}
	return value
}

// Explicit rule/receipt headers override the workspace default.
func applyWorkspaceServer(result, workspace Doc) {
	headers := object(result["headers"])
	if headers == nil {
		headers = Doc{}
		result["headers"] = headers
	}
	for key := range headers {
		if strings.EqualFold(key, "Server") {
			return
		}
	}
	if value := workspaceServerHeader(workspace); value != "" {
		headers["Server"] = value
	}
}

func (a *draftAgent) responseSettingsTool(name string, args Doc) (Doc, error) {
	if a.workspaceID == "" {
		fail(400, "请在已保存的工作区内设置响应头")
	}
	workspace := get(a.manager.store.db, "workspaces", a.workspaceID)
	state := func() Doc {
		return Doc{"workspace_id": workspace["id"], "version": workspace["version"], "server_header": workspaceServerHeader(workspace), "notice": "Server 为当前工作区默认响应头，空字符串表示不发送；接口或回传单独设置的 Server 优先。保存后同步已发布端口。"}
	}
	if name == "read_response_settings" {
		a.responseSettingsVersion = integer(workspace["version"])
		return state(), nil
	}
	version, ok := number(args["version"])
	if !ok || a.responseSettingsVersion < 1 || version != int64(a.responseSettingsVersion) {
		fail(409, "请先 read_response_settings，再使用返回的工作区版本修改")
	}
	if integer(workspace["version"]) != a.responseSettingsVersion {
		fail(409, "工作区已更新，请重新 read_response_settings，避免覆盖其他修改")
	}
	if _, ok := args["server_header"]; !ok {
		fail(400, "请提供 server_header；空字符串表示不发送该响应头")
	}
	value := validateServerHeader(args)
	if value == workspaceServerHeader(workspace) {
		return merge(state(), Doc{"ok": true, "changed": false, "persistence": a.persistenceState()}), nil
	}
	workspace["server_header"] = value
	workspace = a.manager.store.saveComposer("workspaces", workspace)
	a.responseSettingsVersion = integer(workspace["version"])
	a.recordMaterialAction("saved", "workspace", workspace)
	return merge(state(), Doc{"ok": true, "changed": true, "live_update": "响应头已保存并同步已有发布端口，无需重复保存或发布。", "persistence": a.persistenceState()}), nil
}

func generationResponseSettingsTools() []Doc {
	return []Doc{
		generationTool("read_response_settings", "Read the current saved workspace's Server response header and version. Empty means omitted. Per-rule/receipt Server overrides take priority; other workspaces are independent.", Doc{}),
		generationTool("set_server_header", "Set only the current workspace's default Server response header as part of requested site authoring or explicit response-header changes. Read read_response_settings first, then use its version. Match the intended simulated HTTP service (e.g. nginx or Apache); do not invent a technology/version from a site name. Empty string omits the header. Applies to pages, static assets, API routes, callbacks and error responses; explicit per-response Server overrides take priority. Saves and hot-updates published ports without first publication or changing security headers.", Doc{"version": Doc{"type": "integer", "minimum": 1}, "server_header": Doc{"type": "string", "maxLength": 200}}, "version", "server_header"),
	}
}
