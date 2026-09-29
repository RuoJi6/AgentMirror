package lab

// Persistence is observed on the server, not inferred from a draft ID. Result
// snapshots intentionally omit IDs; they may still equal a saved workspace
// material. This metadata never grants edit authority or changes the draft.
func (a *draftAgent) persistenceState() Doc {
	state := Doc{"saved_changes_auto_sync": true, "notice": "保存成功的素材变更会自动同步引用它的已发布工作区，无需重复保存或发布；不要根据草稿 id 缺失断言工作区素材未保存。仅说明本轮实际变化及真实未完成项。"}
	if a.manager == nil || a.manager.store == nil {
		return state
	}
	q := a.manager.store.db
	w := entityMaybe(q, "workspaces", a.workspaceID)
	candidates := map[string][]string{"site": {}, "scenario": {}}
	if w != nil {
		dep := entityMaybe(q, "deployments", str(w["deployment_id"]))
		published := dep != nil && dep["mode"] == "composable" && dep["workspace_id"] == w["id"]
		ports, active := 0, 0
		for _, listener := range listing(q, "listeners") {
			if dep != nil && listener["deployment_id"] == dep["id"] {
				ports++
				if boolean(listener["enabled"]) && boolean(dep["enabled"]) {
					active++
				}
			}
		}
		state["workspace"] = Doc{"id": w["id"], "name": w["name"], "version": w["version"], "published": published, "requires_first_publish": !published, "bound_ports": ports, "active_ports": active}
		for _, node := range workspaceSiteNodes(w) {
			if id := str(node["site_id"]); id != "" {
				candidates["site"] = append(candidates["site"], id)
			}
		}
		for _, raw := range array(w["bindings"]) {
			candidates["scenario"] = append(candidates["scenario"], str(object(raw)["scenario_id"]))
		}
	}
	for _, raw := range a.materialActions {
		d := object(raw)
		if d["action"] == "saved" && (d["kind"] == "site" || d["kind"] == "scenario") {
			kind := str(d["kind"])
			candidates[kind] = append(candidates[kind], str(d["id"]))
		}
	}
	drafts := Doc{}
	for _, module := range []struct {
		kind    string
		draft   Doc
		initial string
	}{{"site", a.site, a.initialSite}, {"scenario", a.scenario, a.initialScenario}} {
		if module.draft == nil {
			continue
		}
		fingerprint, valid := persistenceModuleSnapshot(module.kind, module.draft)
		changed := !valid || fingerprint != module.initial
		item := Doc{"changed_since_last_save": changed, "state": "context_only", "requires_save": nil}
		if changed {
			item["state"], item["requires_save"] = "unsaved_changes", true
		}
		ids := candidates[module.kind]
		if id := str(module.draft["id"]); id != "" {
			ids = append([]string{id}, ids...)
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			saved := entityMaybe(q, referenceEntityKind(module.kind), id)
			if saved == nil {
				continue
			}
			if valid && generationModuleSnapshot(module.kind, saved) == fingerprint {
				merge(item, Doc{"state": "saved", "requires_save": false, "saved_id": id, "saved_version": saved["version"], "saved_name": saved["name"]})
				break
			}
		}
		drafts[module.kind] = item
	}
	state["drafts"] = drafts
	return state
}

// Incremental tools may temporarily leave a new site without its entry file.
// Reporting persistence must not fail the next read or claim invalid data saved.
func persistenceModuleSnapshot(kind string, doc Doc) (snapshot string, valid bool) {
	defer func() {
		if recover() != nil {
			valid = false
		}
	}()
	return generationModuleSnapshot(kind, doc), true
}
