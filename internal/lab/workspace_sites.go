package lab

import (
	"net/url"
	"strings"
)

const workspaceRootSite = "root"

// The main site remains site_id for old clients. Child mounts form a validated
// tree; generated absolute paths are never accepted from untrusted callers.
func normalizeSiteMounts(q queryer, raw any) []any {
	if raw == nil {
		return []any{}
	}
	values := array(raw)
	if values == nil || len(values) > 16 {
		fail(400, "子站点必须为数组，最多 16 个")
	}
	out := []any{}
	byID := map[string]Doc{}
	for _, value := range values {
		d := object(value)
		id := textField(d, "id", 64, false)
		if id == "" {
			id = randomHex(6)
		}
		if !idPattern.MatchString(id) || id == workspaceRootSite || byID[id] != nil {
			fail(400, "子站点编号无效或重复")
		}
		site := get(q, "sites", textField(d, "site_id", 64, true))
		segment := textField(d, "segment", 60, true)
		if !slugPattern.MatchString(segment) {
			fail(400, "子站点路径段仅支持小写字母、数字和连字符")
		}
		parent := textField(d, "parent_id", 64, false)
		if parent == "" {
			parent = workspaceRootSite
		}
		item := Doc{"id": id, "name": textField(d, "name", 80, true), "site_id": site["id"], "parent_id": parent, "segment": segment, "server_header": validateServerHeader(d)}
		byID[id] = item
		out = append(out, item)
	}
	seen := map[string]bool{}
	visiting := map[string]bool{}
	var resolve func(string, int) string
	resolve = func(id string, depth int) string {
		if id == workspaceRootSite {
			return ""
		}
		node := byID[id]
		if node == nil {
			fail(400, "子站点的上级不存在")
		}
		if depth > 8 || visiting[id] {
			fail(400, "子站点不能循环引用，嵌套层数不得超过 8")
		}
		if p := str(node["mount_path"]); p != "" {
			return strings.TrimSuffix(p, "/")
		}
		visiting[id] = true
		p := resolve(str(node["parent_id"]), depth+1) + "/" + str(node["segment"])
		visiting[id] = false
		if strings.Count(p, "/") > 8 {
			fail(400, "子站点嵌套层数不得超过 8")
		}
		if seen[p] {
			fail(409, "子站点路径重复："+p)
		}
		seen[p] = true
		node["mount_path"] = p + "/"
		return p
	}
	for id := range byID {
		resolve(id, 1)
	}
	return out
}
func workspaceSiteNodes(workspace Doc) []Doc {
	out := []Doc{{"id": optional(workspace, "root_site_node_id", workspaceRootSite), "name": "主站点", "site_id": workspace["site_id"], "mount_path": "/", "server_header": workspace["server_header"]}}
	for _, raw := range array(workspace["site_mounts"]) {
		out = append(out, object(raw))
	}
	return out
}
func workspaceUsesSite(workspace Doc, id string) bool {
	for _, node := range workspaceSiteNodes(workspace) {
		if node["site_id"] == id {
			return true
		}
	}
	return false
}
func compileWorkspaceBindings(q queryer, workspace Doc) []Doc {
	bindings := normalizeBindings(q, workspace["bindings"])
	prefixes := map[string]string{workspaceRootSite: ""}
	for _, n := range array(workspace["site_mounts"]) {
		node := object(n)
		prefixes[str(node["id"])] = strings.TrimSuffix(str(node["mount_path"]), "/")
	}
	for _, raw := range bindings {
		b := object(raw)
		id := str(b["site_node_id"])
		if id == "" {
			id = workspaceRootSite
		}
		prefix, ok := prefixes[id]
		if !ok {
			fail(400, "场景实例引用的站点已不存在，请调整所属站点")
		}
		if prefix == "" {
			continue
		}
		scenario := get(q, "scenarios", str(b["scenario_id"]))
		paths := object(b["paths"])
		for _, value := range array(scenario["rules"]) {
			rule := object(value)
			rid := str(rule["id"])
			local := str(fallback(paths[rid], rule["path"]))
			paths[rid] = prefix + local
		}
	}
	rules := compileBindings(q, bindings)
	for _, rule := range rules {
		node := workspaceNodeForPath(workspace, str(rule["path"]))
		if str(node["id"]) != str(optional(rule, "site_node_id", workspaceRootSite)) {
			fail(409, "场景接口进入了其他子站点路径，请调整所属站点或路径："+str(rule["path"]))
		}
	}
	return rules
}
func workspaceSiteVersions(q queryer, workspace Doc) []any {
	out := []any{}
	for _, node := range workspaceSiteNodes(workspace)[1:] {
		site := get(q, "sites", str(node["site_id"]))
		snapshot := clone(node)
		snapshot["site_version_id"] = versionKey(site)
		out = append(out, snapshot)
	}
	return out
}
func validateWorkspaceSiteRoutes(q queryer, workspace Doc, rules []Doc) {
	validateWorkspaceListeners(q, workspace, rules)
	callback := callbackPath(workspace)
	for _, node := range workspaceSiteNodes(workspace) {
		site := entityMaybe(q, "sites", str(node["site_id"]))
		prefix := strings.TrimSuffix(str(node["mount_path"]), "/")
		if prefix == "" {
			validateCallbackRoutes(callback, rules, site)
			continue
		}
		if callback == prefix || strings.HasPrefix(callback, prefix+"/") || prefix == "/health" {
			fail(409, "子站点路径与保留接口冲突："+prefix)
		}
		// Explicit parent files must not silently become inaccessible under a mount.
		for _, parent := range workspaceSiteNodes(workspace) {
			parentPrefix := strings.TrimSuffix(str(parent["mount_path"]), "/")
			if parentPrefix == prefix || !strings.HasPrefix(prefix+"/", parentPrefix+"/") {
				continue
			}
			psite := entityMaybe(q, "sites", str(parent["site_id"]))
			for _, raw := range array(psite["files"]) {
				path := parentPrefix + "/" + str(object(raw)["path"])
				if path == prefix || strings.HasPrefix(path, prefix+"/") {
					fail(409, "子站点路径遮挡了上级站点文件："+path)
				}
			}
		}
	}
}

// Longest mount wins. Static lookup is confined to that site; a missing child
// file must not leak through to a parent's SPA fallback.
func workspaceNodeForPath(workspace Doc, requested string) Doc {
	node := workspaceSiteNodes(workspace)[0]
	for _, raw := range array(workspace["site_mounts"]) {
		candidate := object(raw)
		prefix := strings.TrimSuffix(str(candidate["mount_path"]), "/")
		if (requested == prefix || strings.HasPrefix(requested, prefix+"/")) && len(prefix) > len(strings.TrimSuffix(str(node["mount_path"]), "/")) {
			node = candidate
		}
	}
	return node
}
func applySiteServer(result, workspace Doc, path string) {
	node := workspaceNodeForPath(workspace, path)
	if str(node["server_header"]) != "" {
		applyWorkspaceServer(result, node)
	} else {
		applyWorkspaceServer(result, workspace)
	}
}

func selectWorkspaceSite(q queryer, snapshot Doc, requested string) (Doc, Doc, string) {
	node := workspaceNodeForPath(snapshot, requested)
	if node["id"] == optional(snapshot, "root_site_node_id", workspaceRootSite) {
		node["site_version_id"] = snapshot["site_version_id"]
	}
	prefix := strings.TrimSuffix(str(node["mount_path"]), "/")
	local := strings.TrimPrefix(requested, prefix)
	if local == "" {
		local = "/"
	}
	site := entityMaybe(q, "sites_versions", str(node["site_version_id"]))
	return site, node, local
}
func mountedSiteEntryLocation(site, node Doc, u *url.URL) string {
	prefix := strings.TrimSuffix(str(node["mount_path"]), "/")
	if prefix != "" && u.Path == prefix {
		return (&url.URL{Path: prefix + "/", RawQuery: u.RawQuery}).String()
	}
	local := *u
	local.Path = strings.TrimPrefix(u.Path, prefix)
	location := siteEntryLocation(site, &local)
	if location != "" {
		return prefix + location
	}
	return ""
}

func reviewMountedSites(snapshot Doc) []Doc {
	out := []Doc{}
	for _, node := range workspaceSiteNodes(object(snapshot["workspace"])) {
		for _, raw := range array(snapshot["sites"]) {
			site := object(raw)
			if node["site_id"] == site["id"] {
				out = append(out, Doc{"site": site, "mount_path": node["mount_path"], "id": node["id"]})
			}
		}
	}
	return out
}
