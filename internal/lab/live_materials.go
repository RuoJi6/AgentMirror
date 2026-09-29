package lab

import (
	"log"
	"strconv"
)

// Each save produces a new immutable release for already published workspaces.
// The transaction includes validation and all dependent releases, so an invalid
// shared edit cannot partially update the listening ports.
func syncSavedMaterials(q queryer, kind, id string) {
	for _, workspace := range listing(q, "workspaces") {
		used := kind == "workspaces" && workspace["id"] == id || kind == "sites" && workspaceUsesSite(workspace, id)
		if kind == "scenarios" {
			for _, value := range array(workspace["bindings"]) {
				used = used || object(value)["scenario_id"] == id
			}
		}
		if used && kind == "scenarios" {
			pruneScenarioBindings(q, workspace, id)
		}
		deployment := entityMaybe(q, "deployments", str(workspace["deployment_id"]))
		if !used || deployment == nil || deployment["mode"] != "composable" || deployment["workspace_id"] != workspace["id"] {
			continue
		}
		for _, other := range listing(q, "deployments") {
			if other["slug"] == workspace["slug"] && other["id"] != deployment["id"] {
				fail(409, "发布路径已被其他部署使用")
			}
		}
		bindings := normalizeBindings(q, workspace["bindings"])
		rules := compileWorkspaceBindings(q, workspace)
		site := entityMaybe(q, "sites", str(workspace["site_id"]))
		validateWorkspaceSiteRoutes(q, workspace, rules)
		mounts := workspaceSiteVersions(q, workspace)
		siteVersion := ""
		if site != nil {
			siteVersion = versionKey(site)
		}
		previous := entityMaybe(q, "composer_releases", str(deployment["release_id"]))
		if previous != nil && previous["site_version_id"] == siteVersion && (len(array(previous["site_mounts"])) == 0 && len(mounts) == 0 || dump(previous["site_mounts"]) == dump(mounts)) && dump(previous["rules"]) == dump(rules) && dump(previous["bindings"]) == dump(bindings) && deployment["name"] == workspace["name"] && deployment["slug"] == workspace["slug"] && callbackPath(deployment) == callbackPath(workspace) && workspaceServerHeader(deployment) == workspaceServerHeader(workspace) && dump(collectRejectedResponseConfig(deployment)) == dump(collectRejectedResponseConfig(workspace)) && dump(collectValidationConfig(deployment)) == dump(collectValidationConfig(workspace)) {
			if integer(workspace["published_version"]) != integer(workspace["version"]) {
				workspace["published_version"] = workspace["version"]
				put(q, "workspaces", workspace)
			}
			continue
		}
		revision := max(integer(deployment["revision"]), integer(workspace["publication_revision"])) + 1
		releaseID := str(deployment["id"]) + "-r" + strconv.Itoa(revision)
		put(q, "composer_releases", Doc{"id": releaseID, "site_version_id": siteVersion, "site_mounts": mounts, "rules": rules, "bindings": bindings, "server_header": workspaceServerHeader(workspace), "callback_path": callbackPath(workspace), "collect_rejected_response": collectRejectedResponseConfig(workspace), "collect_validation": collectValidationConfig(workspace), "created_at": timestamp(), "source": "save"})
		merge(deployment, Doc{"release_id": releaseID, "revision": revision, "name": workspace["name"], "slug": workspace["slug"], "site_id": workspace["site_id"], "site_version": site["version"], "server_header": workspaceServerHeader(workspace), "callback_path": callbackPath(workspace), "collect_rejected_response": collectRejectedResponseConfig(workspace), "collect_validation": collectValidationConfig(workspace), "updated_at": timestamp()})
		// Saving must never enable a paused deployment or alter listener bindings.
		put(q, "deployments", deployment)
		merge(workspace, Doc{"publication_revision": revision, "published_version": workspace["version"], "published_at": timestamp()})
		put(q, "workspaces", workspace)
	}
}

// Refresh only the request's in-memory view; the original session and prior
// deliveries remain evidence of what was actually served at that time.
func refreshSessionMaterials(q queryer, session, deployment Doc) {
	snap := object(session["snapshot"])
	release := get(q, "composer_releases", str(deployment["release_id"]))
	snap["deployment"] = clone(deployment)
	applyListenerRelease(snap, release, object(snap["listener"]))
}

// Removing a rule also removes its obsolete overrides, including in unpublished
// workspaces. Unrelated overrides are preserved and stale editors see a conflict.
func pruneScenarioBindings(q queryer, workspace Doc, scenarioID string) {
	valid := map[string]bool{}
	for _, v := range array(get(q, "scenarios", scenarioID)["rules"]) {
		valid[str(object(v)["id"])] = true
	}
	changed := false
	for _, v := range array(workspace["bindings"]) {
		b := object(v)
		if b["scenario_id"] != scenarioID {
			continue
		}
		for _, field := range []string{"paths", "rule_profiles"} {
			for id := range object(b[field]) {
				if !valid[id] {
					delete(object(b[field]), id)
					changed = true
				}
			}
		}
	}
	if changed {
		workspace["version"] = integer(workspace["version"]) + 1
		workspace["updated_at"] = timestamp()
		put(q, "workspaces", workspace)
	}
}

// Upgrade existing published workspaces to saved-material semantics. Idempotent
// comparison avoids new releases on every restart. Invalid legacy drafts leave
// their last working release available; every workspace is its own transaction.
func (s *Store) reconcileSavedMaterials() {
	for _, workspace := range listing(s.db, "workspaces") {
		if str(workspace["deployment_id"]) == "" {
			continue
		}
		func() {
			defer func() {
				if p := recover(); p != nil {
					log.Printf("saved material sync skipped for workspace %s: %v", workspace["id"], caught(p))
				}
			}()
			s.write(func(q queryer) {
				current := get(q, "workspaces", str(workspace["id"]))
				for _, v := range array(current["bindings"]) {
					pruneScenarioBindings(q, current, str(object(v)["scenario_id"]))
				}
				syncSavedMaterials(q, "workspaces", str(current["id"]))
			})
		}()
	}
}
