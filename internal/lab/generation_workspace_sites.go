package lab

import "strings"

func (a *draftAgent) workspaceSitesState(w Doc) Doc {
	q := a.manager.store.db
	nodes := []any{}
	a.workspaceSiteVersions = map[string]int{}
	a.workspacePortSnapshots = map[string]string{}
	ports := []any{}
	for _, l := range listing(q, "listeners") {
		if str(w["deployment_id"]) != "" && l["deployment_id"] == w["deployment_id"] {
			a.workspacePortSnapshots[str(l["id"])] = dump(l)
			ports = append(ports, clone(l))
		}
	}
	for _, n := range workspaceSiteNodes(w) {
		item := clone(n)
		item["site_version"] = 0
		site := entityMaybe(q, "sites", str(n["site_id"]))
		if site != nil {
			a.workspaceSiteVersions[str(site["id"])] = integer(site["version"])
			item["site_version"], item["site_name"] = site["version"], site["name"]
			files := []any{}
			for _, raw := range array(site["files"]) {
				f := object(raw)
				if f["encoding"] == "hosted" {
					files = append(files, clone(f))
				}
			}
			item["hosted_files"] = files
		}
		urls := []any{}
		for _, raw := range ports {
			l := object(raw)
			for _, root := range workspaceSiteNodes(w) {
				if root["id"] == listenerSiteID(l) && strings.HasPrefix(str(n["mount_path"]), str(root["mount_path"])) {
					urls = append(urls, Doc{"listener_id": l["id"], "enabled": l["enabled"], "url": strings.TrimSuffix(str(l["public_url"]), "/") + "/" + strings.TrimPrefix(str(n["mount_path"]), str(root["mount_path"]))})
				}
			}
		}
		item["entry_urls"] = urls
		nodes = append(nodes, item)
	}
	a.workspaceSitesVersion = integer(w["version"])
	return Doc{"workspace_id": w["id"], "version": w["version"], "deployment_id": w["deployment_id"], "site_nodes": nodes, "ports": ports, "notice": "当前已保存状态。素材内容通过 read_material/load_material 编辑；工作区和端口写入成功即生效。只操作用户要求的节点/端口。"}
}

func (a *draftAgent) workspaceSitesTool(name string, args Doc) (Doc, error) {
	if name == "list_hosted_files" {
		// Metadata only; files are uploaded by the user, never read from host paths.
		items := []any{}
		query := strings.ToLower(textField(args, "query", 200, false))
		for _, f := range listing(a.manager.store.db, "hosted_files") {
			if query == "" || strings.Contains(strings.ToLower(str(f["name"])), query) {
				items = append(items, f)
			}
			if len(items) >= 100 {
				break
			}
		}
		return Doc{"items": items, "limit": 100, "notice": "只有已上传文件的元信息；需要的文件不存在时用 ask_user 请用户上传，不伪造软件安装包。"}, nil
	}
	if a.workspaceID == "" {
		fail(400, "请先保存当前工作区")
	}
	s := a.manager.store
	w := get(s.db, "workspaces", a.workspaceID)
	if name == "read_workspace_sites" {
		return a.workspaceSitesState(w), nil
	}
	version, ok := number(args["version"])
	if !ok || a.workspaceSitesVersion < 1 || version != int64(a.workspaceSitesVersion) {
		fail(409, "请先 read_workspace_sites，使用最新版本操作")
	}
	guard := func(q queryer) {
		if integer(get(q, "workspaces", a.workspaceID)["version"]) != int(version) {
			fail(409, "工作区已变更，请重新 read_workspace_sites")
		}
	}
	guard(s.db)
	finish := func(extra Doc) Doc {
		return merge(a.workspaceSitesState(get(s.db, "workspaces", a.workspaceID)), extra)
	}
	switch name {
	case "upsert_workspace_site", "delete_workspace_site":
		id := textField(args, "site_node_id", 64, name == "delete_workspace_site")
		nodes := array(w["site_mounts"])
		index := -1
		for i, n := range nodes {
			if object(n)["id"] == id {
				index = i
			}
		}
		if name == "delete_workspace_site" {
			if id == workspaceRootSite || index < 0 {
				fail(400, "请选择当前工作区的子站点")
			}
			nodes = append(nodes[:index], nodes[index+1:]...)
		} else {
			patch, err := parseAgentJSON(textField(args, "json", 4000, true))
			if err != nil {
				return nil, err
			}
			for k := range patch {
				if !has([]any{"name", "site_id", "parent_id", "segment", "server_header"}, k) {
					fail(400, "不支持的站点字段："+k)
				}
			}
			if id == workspaceRootSite {
				for k := range patch {
					if k != "site_id" && k != "server_header" {
						fail(400, "主站点仅支持替换 site_id 和 server_header")
					}
				}
				merge(w, patch)
			} else {
				if id != "" && index < 0 {
					fail(404, "子站点不存在；新增时省略 site_node_id")
				}
				if index < 0 {
					id = randomHex(6)
					nodes = append(nodes, merge(Doc{"id": id, "parent_id": "root"}, patch))
				} else {
					merge(object(nodes[index]), patch)
				}
			}
		}
		w["site_mounts"] = nodes
		saved := s.saveComposer("workspaces", w)
		a.recordMaterialAction("saved", "workspace", saved)
		return finish(Doc{"ok": true, "site_node_id": id, "saved": true}), nil
	case "publish_workspace":
		dep := s.publishWorkspace(a.workspaceID, int(version))
		a.recordMaterialAction("saved", "workspace", get(s.db, "workspaces", a.workspaceID))
		return finish(Doc{"ok": true, "deployment_id": dep["id"], "saved": true}), nil
	case "set_workspace_port", "delete_workspace_port":
		lm := a.manager.listeners
		if lm == nil {
			fail(503, "端口管理器不可用")
		}
		if str(w["deployment_id"]) == "" {
			fail(409, "工作区尚未发布；仅在用户要求发布时调用 publish_workspace")
		}
		id := textField(args, "listener_id", 64, name == "delete_workspace_port")
		portGuard := func(q queryer) {
			guard(q)
			if id != "" {
				l := get(q, "listeners", id)
				if l["deployment_id"] != w["deployment_id"] || a.workspacePortSnapshots[id] == "" {
					fail(403, "只能操作 read_workspace_sites 返回的当前工作区端口")
				}
				if dump(l) != a.workspacePortSnapshots[id] {
					fail(409, "端口配置已变更，请重新读取")
				}
			}
		}
		if name == "delete_workspace_port" {
			lm.delete(id, portGuard)
		} else {
			patch, err := parseAgentJSON(textField(args, "json", 4000, true))
			if err != nil {
				return nil, err
			}
			for k := range patch {
				if !has([]any{"name", "host", "port", "public_url", "site_node_id", "enabled"}, k) {
					fail(400, "不支持的端口字段："+k)
				}
			}
			if id == "" {
				for _, key := range []string{"name", "host", "port", "public_url", "site_node_id"} {
					if patch[key] == nil || (key != "port" && str(patch[key]) == "") {
						fail(400, "新增端口缺少 "+key+"，请先向用户确认")
					}
				}
			}
			func() {
				lm.mu.Lock()
				defer lm.mu.Unlock()
				raw := Doc{"deployment_id": w["deployment_id"], "root_surface": "page", "enabled": true}
				if id != "" {
					portGuard(s.db)
					raw = clone(get(s.db, "listeners", id))
				}
				merge(raw, patch)
				saved := lm.saveLocked(raw, portGuard)
				id = str(saved["id"])
			}()
		}
		a.recordMaterialAction("saved", "workspace", w)
		return finish(Doc{"ok": true, "listener_id": id, "saved": true}), nil
	case "attach_hosted_file":
		result := s.attachWorkspaceHostedFile(a.workspaceID, args)
		saved := object(result["site"])
		a.recordMaterialAction("saved", "site", saved)
		a.recordMaterialAction("saved", "workspace", object(result["workspace"]))
		state := finish(Doc{"ok": true, "saved": true, "site_id": saved["id"], "site_version": saved["version"], "path": result["path"]})
		links := []any{}
		for _, raw := range array(state["site_nodes"]) {
			n := object(raw)
			if n["id"] == args["site_node_id"] {
				for _, item := range array(n["entry_urls"]) {
					u := clone(object(item))
					u["url"] = str(u["url"]) + hostedDownloadPath(str(result["path"]))
					links = append(links, u)
				}
			}
		}
		state["download_urls"] = links
		if len(links) == 0 {
			state["notice"] = "文件已托管并保存，但当前站点尚未绑定 HTTP 端口。需要可访问下载地址时向用户确认端口和公告地址，再发布绑定；不要报告不存在的 URL。"
		}
		return state, nil
	case "remove_hosted_file":
		nodeID := textField(args, "site_node_id", 64, true)
		var node Doc
		for _, n := range workspaceSiteNodes(w) {
			if n["id"] == nodeID {
				node = n
				break
			}
		}
		if node == nil {
			fail(404, "站点节点不存在")
		}
		site := get(s.db, "sites", str(node["site_id"]))
		if integer(args["site_version"]) != a.workspaceSiteVersions[str(site["id"])] || integer(site["version"]) != integer(args["site_version"]) {
			fail(409, "站点素材已变更，请重新 read_workspace_sites")
		}
		path := sitePath(textField(args, "path", 512, true))
		files := array(site["files"])
		index := -1
		for i, f := range files {
			if object(f)["path"] == path {
				index = i
			}
		}
		if index < 0 || object(files[index])["encoding"] != "hosted" {
			fail(404, "此路径不是托管下载")
		}
		files = append(files[:index], files[index+1:]...)
		site["files"] = files
		saved := s.saveComposer("sites", site, guard)
		a.recordMaterialAction("saved", "site", saved)
		return finish(Doc{"ok": true, "saved": true, "site_id": saved["id"], "site_version": saved["version"], "path": path}), nil
	}
	fail(400, "未知多站点操作")
	return nil, nil
}

func generationWorkspaceSitesTools() []Doc {
	s := func(d string) Doc { return Doc{"type": "string", "description": d} }
	v := Doc{"type": "integer", "minimum": 1, "description": "Latest version returned by read_workspace_sites or successful workspace operation"}
	return []Doc{
		generationTool("read_workspace_sites", "Read CURRENT workspace site tree, material versions, hosted-file references, bound ports and entry URLs. Required before topology, hosting or port writes. No file or prompt bodies.", Doc{}),
		generationTool("upsert_workspace_site", "Add or patch one site node in CURRENT workspace. Omit site_node_id only for new child. json fields: name, site_id, parent_id, segment, server_header. root supports site_id/server_header only. Saves immediately; never changes bindings, files or ports.", Doc{"version": v, "site_node_id": s("Node ID; omit to create"), "json": s("Only changed fields; new child needs name, site_id and segment")}, "version", "json"),
		generationTool("delete_workspace_site", "Remove an explicitly requested child mount only. Descendants, scenario bindings and ports must first be resolved with the user; never cascade or delete materials.", Doc{"version": v, "site_node_id": s("Exact child ID")}, "version", "site_node_id"),
		generationTool("publish_workspace", "Publish CURRENT saved workspace only when user explicitly requests publication. Does not bind or enable ports. Read current version first.", Doc{"version": v}, "version"),
		generationTool("set_workspace_port", "Create or patch a port of CURRENT workspace only, including selecting a child as /. Use only for explicitly requested deployment/port changes. New requires name, host, port, public_url and site_node_id. Ask user for missing address/port; never guess or reassign another workspace's listener. Patch preserves omitted fields.", Doc{"version": v, "listener_id": s("Current-workspace listener ID; omit to create"), "json": s("Fields: name, host, port, public_url, site_node_id, enabled")}, "version", "json"),
		generationTool("delete_workspace_port", "Delete an explicitly requested port of CURRENT workspace. Stops listening; does not delete site/material/history.", Doc{"version": v, "listener_id": s("Exact listener ID read first")}, "version", "listener_id"),
		generationTool("list_hosted_files", "Find already uploaded file metadata (max 100) by name. Never reads host filesystem or binary contents. Files attached in chat are already uploaded; use attachments IDs directly. If missing, ask_user to upload in chat.", Doc{"query": s("Optional name search")}),
		generationTool("attach_hosted_file", "Attach a chat upload or an existing uploaded file to one site's download path. Requires versions from read_workspace_sites. For an empty root, site_version=0 atomically creates a download page and selects it; users do not need to create a main site first. Returns actual bound download URLs, or indicates a port is still required. Site material may be shared; all references receive the change. Refuses overwrite. Saves/hot-updates immediately; no binary data in arguments.", Doc{"version": v, "site_version": Doc{"type": "integer", "minimum": 0, "description": "Version from read_workspace_sites; 0 for empty root"}, "site_node_id": s("root or child node ID"), "path": s("Relative download path"), "file_id": s("ID from list_hosted_files"), "download_name": s("Optional download filename")}, "version", "site_version", "site_node_id", "path", "file_id"),
		generationTool("remove_hosted_file", "Remove one explicitly requested hosted download reference from a site's material. Preserves archived file bytes. Refuses non-hosted paths.", Doc{"version": v, "site_version": v, "site_node_id": s("root or child node ID"), "path": s("Exact relative download path")}, "version", "site_version", "site_node_id", "path"),
	}
}
