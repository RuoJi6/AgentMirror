package lab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"

	htmlparser "golang.org/x/net/html"
)

// Legacy entity readers and public renderers stay available for existing
// listeners and immutable session snapshots. Their authoring API is retired;
// all new deployments are produced by publishing a workspace.
func retiredAuthoringPath(path string) bool {
	for _, prefix := range []string{"/api/templates", "/api/deployments", "/api/preview"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func deploymentLists(q queryer) ([]Doc, []Doc) {
	current, legacy := []Doc{}, []Doc{}
	for _, doc := range listing(q, "deployments") {
		if doc["mode"] == "composable" {
			current = append(current, doc)
		} else {
			// Existing ports need an identity, not a second editable business model.
			legacy = append(legacy, Doc{"id": doc["id"], "name": doc["name"], "slug": doc["slug"], "enabled": doc["enabled"], "legacy": true})
		}
	}
	return current, legacy
}

func legacyDeploymentReferenced(q queryer, deployment Doc) bool {
	for _, listener := range listing(q, "listeners") {
		if listener["deployment_id"] == deployment["id"] {
			return true
		}
		if !boolean(listener["legacy_paths"]) {
			continue
		}
		bound := entityMaybe(q, "deployments", str(listener["deployment_id"]))
		if bound != nil && bound["mode"] != "composable" {
			// Migrated legacy ports can start sessions for every old /h/<slug>.
			// Paused ports retain that capability when resumed. Rebinding the port
			// to a workspace bypasses legacy dispatch, even if the flag remains.
			return true
		}
	}
	return false
}

func (s *Store) migrateLegacyTemplates() {
	s.write(func(q queryer) {
		for _, template := range listing(q, "templates") {
			id := str(template["id"])
			if entityMaybe(q, "legacy_template_migrations", id) != nil {
				continue
			}
			site, err := legacyTemplateSite(template)
			if err != nil {
				// A malformed historical template must not prevent access to the
				// original record or experiment history. Retry on the next startup.
				log.Printf("legacy template migration skipped for %q: %v", id, err)
				continue
			}
			digest := sha256.Sum256([]byte(id))
			siteID := "legacy-" + hex.EncodeToString(digest[:8])
			for entityMaybe(q, "sites", siteID) != nil {
				siteID = "legacy-" + randomHex(8)
			}
			now := timestamp()
			merge(site, Doc{"id": siteID, "version": 1, "created_at": now, "updated_at": now})
			put(q, "sites", site)
			immutable := clone(site)
			immutable["id"], immutable["source_id"] = versionKey(site), siteID
			put(q, "sites_versions", immutable)
			// Keep the marker even if the new site is later removed intentionally.
			put(q, "legacy_template_migrations", Doc{"id": id, "site_id": siteID, "migrated_at": now, "mode": "static_appearance"})
		}
	})
}

func legacyTemplateSite(template Doc) (site Doc, err error) {
	defer func() {
		if p := recover(); p != nil {
			site, err = nil, caught(p)
		}
	}()
	document, parseErr := htmlparser.Parse(strings.NewReader(page(template, nil, false, false)))
	check(parseErr)
	var clean func(*htmlparser.Node)
	clean = func(node *htmlparser.Node) {
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == htmlparser.ElementNode && child.Data == "script" {
				node.RemoveChild(child)
			} else {
				clean(child)
			}
			child = next
		}
		if node.Type != htmlparser.ElementNode {
			return
		}
		attrs := []htmlparser.Attribute{}
		for _, attr := range node.Attr {
			if strings.HasPrefix(strings.ToLower(attr.Key), "on") {
				continue
			}
			if node.Data == "form" && (attr.Key == "action" || attr.Key == "method") {
				continue
			}
			if attr.Key == "href" && (strings.HasPrefix(attr.Val, "/portal/") || strings.HasPrefix(attr.Val, "/assets/client.js")) {
				attr.Val = "#"
			}
			attrs = append(attrs, attr)
		}
		node.Attr = attrs
		if node.Data == "form" {
			// A migrated login is appearance only. It must not submit credentials
			// or imply that its old authentication/delivery flow was migrated.
			node.Data = "div"
		}
	}
	clean(document)
	var rendered bytes.Buffer
	check(htmlparser.Render(&rendered, document))
	return validateSite(Doc{"name": template["name"], "entry": "index.html", "spa": false, "source": "legacy-template:" + str(template["id"]) + " (static appearance; reconnect actions in a workspace)", "files": []any{Doc{"path": "index.html", "encoding": "utf8", "content": rendered.String()}}}), nil
}

// Called only by the management entry points; internal compatibility fixtures
// can still construct old listeners through saveLocked without opening an API.
func managedListenerConfig(q queryer, raw Doc) Doc {
	result := clone(raw)
	deploymentID := str(raw["deployment_id"])
	if deploymentID == "" {
		return result
	}
	deployment := get(q, "deployments", deploymentID)
	if deployment["mode"] == "composable" {
		return result
	}
	old := entityMaybe(q, "listeners", str(raw["id"]))
	if old == nil || old["deployment_id"] != deploymentID {
		fail(400, "新增或改绑端口请选择蜜罐工作区发布的部署")
	}
	if value, exists := raw["root_surface"]; exists && str(value) != str(fallback(old["root_surface"], "page")) {
		fail(400, "旧部署仅保留运行兼容，不能修改旧页面或投放配置")
	}
	if value, exists := raw["overrides"]; exists && dump(value) != dump(old["overrides"]) {
		fail(400, "旧部署仅保留运行兼容，不能修改旧页面或投放配置")
	}
	result["root_surface"], result["overrides"] = fallback(old["root_surface"], "page"), old["overrides"]
	return result
}
