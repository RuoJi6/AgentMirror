package lab

import "strings"

func callbackPath(doc Doc) string {
	if value := str(doc["callback_path"]); value != "" {
		return value
	}
	return "/collect"
}

func validateCallbackPath(value string) string {
	if value == "" {
		value = "/collect"
	}
	if len(value) > 256 || value == "/" {
		fail(400, "回传路径需为 256 字符以内的独立绝对路径")
	}
	if value != "/collect" {
		validateScenarioPath(value)
	}
	return value
}

func validateCallbackRoutes(callback string, rules []Doc, site Doc) {
	for _, rule := range rules {
		if rule["path"] == callback {
			fail(409, "回传路径与模拟接口冲突："+callback)
		}
	}
	for _, raw := range array(site["files"]) {
		if "/"+str(object(raw)["path"]) == callback {
			fail(409, "回传路径与站点文件冲突："+callback)
		}
	}
}

func listenerCallbackURL(listener, deployment Doc) string {
	return strings.TrimRight(str(listener["public_url"]), "/") + callbackPath(deployment)
}

// Agent edits only the current workspace's callback path, with a read/version
// guard; it cannot rebind ports, change an origin, or publish a new workspace.
func (a *draftAgent) callbackTool(name string, args Doc) (Doc, error) {
	if a.workspaceID == "" {
		fail(400, "请在已保存的工作区内设置回传路径")
	}
	if name == "read_callback" {
		workspace := get(a.manager.store.db, "workspaces", a.workspaceID)
		a.callbackVersion = integer(workspace["version"])
		addresses := []any{}
		for _, listener := range listing(a.manager.store.db, "listeners") {
			if workspace["deployment_id"] != nil && listener["deployment_id"] == workspace["deployment_id"] {
				addresses = append(addresses, Doc{"listener_id": listener["id"], "url": listenerCallbackURL(listener, workspace)})
			}
		}
		return Doc{"workspace_id": workspace["id"], "version": workspace["version"], "callback_path": callbackPath(workspace), "collect_rejected_response": collectRejectedResponseConfig(workspace), "addresses": addresses, "token_policy": "每次业务请求轮换，只接受当前会话最新令牌"}, nil
	}
	if a.callbackVersion == 0 || integer(args["version"]) != a.callbackVersion {
		fail(409, "请先 read_callback，再使用返回的工作区版本修改")
	}
	workspace := get(a.manager.store.db, "workspaces", a.workspaceID)
	if integer(workspace["version"]) != a.callbackVersion {
		fail(409, "工作区已更新，请重新 read_callback，避免覆盖其他修改")
	}
	if name == "set_callback_path" {
		workspace["callback_path"] = validateCallbackPath(textField(args, "path", 256, true))
	} else {
		patch, err := parseAgentJSON(textField(args, "json", scenarioBodyLimit, true))
		if err != nil {
			return nil, err
		}
		workspace["collect_rejected_response"] = validateCollectResponse(merge(collectRejectedResponseConfig(workspace), patch))
	}
	saved := a.manager.store.saveComposer("workspaces", workspace)
	a.callbackVersion = integer(saved["version"])
	a.recordMaterialAction("saved", "workspace", saved)
	return Doc{"ok": true, "workspace_id": saved["id"], "version": saved["version"], "callback_path": callbackPath(saved), "collect_rejected_response": collectRejectedResponseConfig(saved), "persistence": a.persistenceState()}, nil
}

func generationCallbackTools() []Doc {
	return []Doc{
		generationTool("read_callback", "Read the CURRENT workspace callback path, version, per-listener callback URLs and token policy. Read-only.", Doc{}),
		generationTool("set_callback_path", "Set only the current workspace callback path when explicitly requested. Read read_callback first and pass its version. Use a local absolute path, never a domain or port. Saving synchronizes already-published listeners; it does not publish an unpublished workspace or change port bindings.", Doc{"path": Doc{"type": "string"}, "version": Doc{"type": "integer", "minimum": 1}}, "path", "version"),
		generationTool("set_callback_response", "Set ONLY the CURRENT workspace's expired/invalid token response. Read read_callback first and pass its version. JSON patch fields: enabled, status (200-599), format (json/text/html), content_type, body, headers. Set enabled=true for a custom response; enabled=false restores default HTTP 403. Only {{run_id}} is substituted. This never accepts invalid tokens or stores rejected submissions. Saving synchronizes this workspace's published ports without changing other workspaces or materials.", Doc{"version": Doc{"type": "integer", "minimum": 1}, "json": Doc{"type": "string"}}, "version", "json"),
	}
}
