package lab

import (
	"html"
	"net/url"
	"strings"
)

func hostedDownloadPath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// Creation of a download page and its workspace binding is one transaction.
// A stale version or route conflict cannot leave partially adopted materials.
func (s *Store) attachWorkspaceHostedFile(workspaceID string, args Doc) Doc {
	var saved Doc
	path := sitePath(textField(args, "path", 512, true))
	nodeID := textField(args, "site_node_id", 64, true)
	fileID := textField(args, "file_id", 64, true)
	version, valid := number(args["version"])
	siteVersion, validSite := number(args["site_version"])
	if !valid || version < 1 || !validSite || siteVersion < 0 {
		fail(400, "请提供工作区和站点版本；未选用素材时 site_version 为 0")
	}
	s.write(func(q queryer) {
		workspace := get(q, "workspaces", workspaceID)
		if integer(workspace["version"]) != int(version) {
			fail(409, "工作区已变更，请刷新后重试")
		}
		var node Doc
		for _, n := range workspaceSiteNodes(workspace) {
			if n["id"] == nodeID {
				node = n
				break
			}
		}
		if node == nil {
			fail(404, "站点节点不存在")
		}
		if expected, supplied := args["site_id"]; supplied && str(expected) != str(node["site_id"]) {
			fail(409, "站点选择尚未保存或已变更，请先保存工作区")
		}
		site := entityMaybe(q, "sites", str(node["site_id"]))
		created := site == nil
		if created && (nodeID != workspaceRootSite || str(node["site_id"]) != "") {
			fail(404, "站点素材不存在")
		}
		if integer(site["version"]) != int(siteVersion) {
			fail(409, "站点素材已变更，请刷新后重试")
		}
		file := get(q, "hosted_files", fileID)
		name := textField(args, "download_name", 200, false)
		if name == "" {
			name = str(file["name"])
		}
		name = hostedFilename(name)
		if created {
			entry := "index.html"
			if path == entry {
				entry = "downloads.html"
			}
			page := `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>文件下载</title><body><h1>文件下载</h1><p><a href="` + html.EscapeString(hostedDownloadPath(path)) + `" download>` + html.EscapeString(name) + `</a></p></body></html>`
			site = Doc{"name": "文件下载", "entry": entry, "files": []any{Doc{"path": entry, "encoding": "utf8", "content": page}}}
		}
		files := array(site["files"])
		for _, f := range files {
			if object(f)["path"] == path {
				fail(409, "此路径已有文件，请选择其他路径或先移除原下载")
			}
		}
		site["files"] = append(files, Doc{"path": path, "encoding": "hosted", "content": "", "file_id": fileID, "download_name": name})
		saved = saveComposerIn(q, "sites", site)
		if created {
			workspace["site_id"] = saved["id"]
			saveComposerIn(q, "workspaces", workspace)
		}
	})
	return Doc{"site": saved, "workspace": workspaceView(s.db, get(s.db, "workspaces", workspaceID)), "path": path}
}
