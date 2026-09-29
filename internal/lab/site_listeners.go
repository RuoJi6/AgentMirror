package lab

import "strings"

func listenerSiteID(listener Doc) string {
	if id := str(listener["site_node_id"]); id != "" {
		return id
	}
	return workspaceRootSite
}

// Project an immutable release onto a listener's selected subtree. Rule and
// node IDs are preserved; only their public paths change. The source is untouched.
func listenerSiteRelease(release, listener Doc) Doc {
	id := listenerSiteID(listener)
	if id == workspaceRootSite {
		return release
	}
	var selected Doc
	for _, raw := range array(release["site_mounts"]) {
		node := object(raw)
		if node["id"] == id {
			selected = node
			break
		}
	}
	if selected == nil {
		fail(404, "端口绑定的子站点不存在，请重新绑定站点")
	}
	prefix := strings.TrimSuffix(str(selected["mount_path"]), "/")
	server := str(selected["server_header"])
	if server == "" {
		server = str(release["server_header"])
	}
	out := Doc{"id": release["id"], "root_site_node_id": id, "site_version_id": selected["site_version_id"], "server_header": server}
	nodes := []any{}
	for _, raw := range array(release["site_mounts"]) {
		node := object(raw)
		if node["id"] != id && strings.HasPrefix(str(node["mount_path"]), prefix+"/") {
			item := clone(node)
			item["mount_path"] = strings.TrimPrefix(str(node["mount_path"]), prefix)
			// Empty headers inherit the workspace, not the new listener root.
			if str(item["server_header"]) == "" {
				item["server_header"] = release["server_header"]
			}
			nodes = append(nodes, item)
		}
	}
	rules := []any{}
	for _, raw := range array(release["rules"]) {
		rule := object(raw)
		if strings.HasPrefix(str(rule["path"]), prefix+"/") {
			item := clone(rule)
			item["path"] = strings.TrimPrefix(str(rule["path"]), prefix)
			rules = append(rules, item)
		}
	}
	out["site_mounts"], out["rules"] = nodes, rules
	return out
}

func applyListenerRelease(snapshot, release, listener Doc) {
	view := listenerSiteRelease(release, listener)
	merge(snapshot, Doc{"release_id": release["id"], "site_version_id": view["site_version_id"], "site_mounts": view["site_mounts"], "rules": view["rules"], "root_site_node_id": listenerSiteID(listener), "server_header": view["server_header"]})
}

func validateListenerSite(q queryer, workspace, listener Doc, rules []Doc) {
	if listenerSiteID(listener) == workspaceRootSite {
		return
	}
	view := listenerSiteRelease(Doc{"site_mounts": workspaceSiteVersions(q, workspace), "rules": rules}, listener)
	callback := callbackPath(workspace)
	projected := []Doc{}
	for _, raw := range array(view["rules"]) {
		rule := object(raw)
		projected = append(projected, rule)
		if rule["path"] == "/health" {
			fail(409, "子站点独立端口的接口与 /health 冲突")
		}
	}
	root := get(q, "sites_versions", str(view["site_version_id"]))
	validateCallbackRoutes(callback, projected, root)
	for _, raw := range array(root["files"]) {
		if object(raw)["path"] == "health" {
			fail(409, "子站点文件与独立端口的 /health 冲突")
		}
	}
	for _, raw := range array(view["site_mounts"]) {
		node := object(raw)
		prefix := strings.TrimSuffix(str(node["mount_path"]), "/")
		if prefix == "/health" || callback == prefix || strings.HasPrefix(callback, prefix+"/") {
			fail(409, "子站点独立端口的路径与保留接口冲突："+prefix)
		}
	}
}

func validateWorkspaceListeners(q queryer, workspace Doc, rules []Doc) {
	if str(workspace["deployment_id"]) == "" {
		return
	}
	for _, listener := range listing(q, "listeners") {
		if listener["deployment_id"] != workspace["deployment_id"] {
			continue
		}
		id := listenerSiteID(listener)
		found := id == workspaceRootSite
		for _, node := range workspaceSiteNodes(workspace) {
			found = found || node["id"] == id
		}
		if !found {
			fail(409, "子站点仍绑定端口，请先改绑或删除端口："+str(listener["name"]))
		}
		validateListenerSite(q, workspace, listener, rules)
	}
}

func listenerServerHeader(q queryer, listener, deployment Doc) string {
	if deployment["mode"] == "composable" && listenerSiteID(listener) != workspaceRootSite {
		release := get(q, "composer_releases", str(deployment["release_id"]))
		for _, raw := range array(release["site_mounts"]) {
			node := object(raw)
			if node["id"] == listenerSiteID(listener) && str(node["server_header"]) != "" {
				return str(node["server_header"])
			}
		}
	}
	return workspaceServerHeader(deployment)
}
