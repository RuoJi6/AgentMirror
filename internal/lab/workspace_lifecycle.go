package lab

func workspaceView(q queryer, workspace Doc) Doc {
	result := clone(workspace)
	result["server_header"] = workspaceServerHeader(workspace)
	result["callback_path"] = callbackPath(workspace)
	result["collect_rejected_response"] = collectRejectedResponseConfig(workspace)
	result["collect_validation"] = collectValidationConfig(workspace)
	result["publication_status"], result["enabled"] = "draft", nil
	if deployment := entityMaybe(q, "deployments", str(workspace["deployment_id"])); deployment != nil && deployment["mode"] == "composable" && deployment["workspace_id"] == workspace["id"] {
		result["enabled"] = boolean(deployment["enabled"])
		result["publication_status"] = "paused"
		if boolean(deployment["enabled"]) {
			result["publication_status"] = "published"
		}
	}
	return result
}

func workspaceListing(q queryer) []Doc {
	items := []Doc{}
	for _, workspace := range listing(q, "workspaces") {
		items = append(items, workspaceView(q, workspace))
	}
	return items
}

func workspaceExpectedVersion(raw Doc) int {
	if value, exists := raw["version"]; exists {
		version, ok := number(value)
		if !ok || version < 1 {
			fail(400, "工作区版本必须为正整数")
		}
		return int(version)
	}
	return 0
}

// Renaming is independent of saving the working draft or publishing a release.
func (s *Store) renameWorkspace(id string, raw Doc) Doc {
	name := textField(raw, "name", 80, true)
	expected := workspaceExpectedVersion(raw)
	if expected == 0 {
		fail(400, "重命名需要提供当前工作区版本")
	}
	var result Doc
	s.write(func(q queryer) {
		workspace := get(q, "workspaces", id)
		if expected != integer(workspace["version"]) {
			fail(409, "工作区已更新，请刷新后再重命名")
		}
		if name != str(workspace["name"]) {
			merge(workspace, Doc{"name": name, "version": expected + 1, "updated_at": timestamp()})
			put(q, "workspaces", workspace)
			deployment := entityMaybe(q, "deployments", str(workspace["deployment_id"]))
			if deployment != nil && deployment["mode"] == "composable" && deployment["workspace_id"] == id {
				merge(deployment, Doc{"name": name, "updated_at": timestamp()})
				put(q, "deployments", deployment)
			}
		}
		result = workspaceView(q, workspace)
	})
	return result
}

func (s *Store) workspaceStatus(id string, raw Doc) Doc {
	enabled, ok := raw["enabled"].(bool)
	if !ok {
		fail(400, "enabled 必须是布尔值")
	}
	expected := workspaceExpectedVersion(raw)
	var result Doc
	s.write(func(q queryer) {
		workspace := get(q, "workspaces", id)
		if expected > 0 && expected != integer(workspace["version"]) {
			fail(409, "草稿已更新，请刷新后重试")
		}
		deployment := entityMaybe(q, "deployments", str(workspace["deployment_id"]))
		if deployment == nil || deployment["mode"] != "composable" || deployment["workspace_id"] != id {
			fail(409, "请先发布工作区，再暂停或恢复发布")
		}
		deployment["enabled"], deployment["updated_at"] = enabled, timestamp()
		put(q, "deployments", deployment)
		result = workspaceView(q, workspace)
	})
	return result
}

func (m *listenerManager) deleteWorkspace(id string) Doc {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		fail(503, "服务正在停止")
	}
	detached := []any{}
	m.app.store.write(func(q queryer) {
		workspace := get(q, "workspaces", id)
		deploymentID := str(workspace["deployment_id"])
		deployment := entityMaybe(q, "deployments", deploymentID)
		if deployment != nil && (deployment["mode"] != "composable" || deployment["workspace_id"] != id) {
			fail(409, "工作区发布引用不一致，未执行删除")
		}
		if deploymentID != "" {
			for _, listener := range listing(q, "listeners") {
				if listener["deployment_id"] == deploymentID {
					merge(listener, Doc{"enabled": false, "deployment_id": nil, "site_node_id": nil, "root_surface": "page", "overrides": nil, "updated_at": timestamp()})
					put(q, "listeners", listener)
					detached = append(detached, listener["id"])
				}
			}
			exec(q, "DELETE FROM entities WHERE kind='deployments' AND id=?", deploymentID)
		}
		exec(q, "DELETE FROM entities WHERE kind IN ('workspaces','review_policies','review_cursors') AND id=?", id)
		// Releases, site versions, profiles and all experiment evidence survive.
		syncDefault(q)
	})
	for _, value := range detached {
		listenerID := str(value)
		if server := m.servers[listenerID]; server != nil {
			delete(m.servers, listenerID)
			m.retire(server)
		}
		delete(m.errors, listenerID)
	}
	return Doc{"ok": true, "detached_listeners": detached}
}
