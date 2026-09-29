package lab

import "strings"

// Binding tools change only the current saved workspace. They use the same
// validation and publication transaction as the editor, never rewrite materials.
func (a *draftAgent) bindingState(workspace Doc) Doc {
	q := a.manager.store.db
	items := []any{}
	for index, raw := range array(workspace["bindings"]) {
		binding := clone(object(raw))
		scenario := get(q, "scenarios", str(binding["scenario_id"]))
		binding["position"] = index + 1
		binding["scenario"] = generationReferenceMetadata("scenario", scenario)
		profile := func(id string) any {
			if id == "" {
				return nil
			}
			return generationReferenceMetadata("profile", generationReferenceEntity(q, "profile", id))
		}
		binding["profile"] = profile(str(binding["profile_id"]))
		prefix := ""
		for _, node := range workspaceSiteNodes(workspace) {
			if node["id"] == binding["site_node_id"] {
				prefix = strings.TrimSuffix(str(node["mount_path"]), "/")
			}
		}
		rules := []any{}
		for _, value := range array(scenario["rules"]) {
			rule := object(value)
			id := str(rule["id"])
			rules = append(rules, Doc{"id": id, "name": rule["name"], "method": rule["method"], "path": prefix + str(fallback(object(binding["paths"])[id], rule["path"])), "original_path": rule["path"], "conditions": rule["conditions"], "delivery_required": rule["delivery_required"], "profile_id": bindingProfileID(binding, id), "profile": profile(bindingProfileID(binding, id)), "inherits_default": str(object(binding["rule_profiles"])[id]) == ""})
		}
		binding["rules"] = rules
		items = append(items, binding)
	}
	return Doc{"workspace_id": workspace["id"], "version": workspace["version"], "bindings": items, "site_nodes": workspaceSiteNodes(workspace), "notice": "这是当前工作区已保存的绑定。实例默认提示词用于未单独指定方案的接口；接口单独指定优先。保存即同步已有发布端口，不修改共享素材或首次发布。"}
}

func (a *draftAgent) bindingTool(name string, args Doc) (Doc, error) {
	if a.workspaceID == "" {
		fail(400, "请在已保存的工作区内操作提示词绑定")
	}
	workspace := get(a.manager.store.db, "workspaces", a.workspaceID)
	if name == "read_bindings" {
		a.bindingVersion = integer(workspace["version"])
		return a.bindingState(workspace), nil
	}
	version, ok := number(args["version"])
	if !ok || version != int64(a.bindingVersion) || a.bindingVersion < 1 {
		fail(409, "请先 read_bindings，再使用返回的工作区版本修改绑定")
	}
	if integer(workspace["version"]) != a.bindingVersion {
		fail(409, "工作区已更新，请重新 read_bindings，避免覆盖其他修改")
	}
	before := dump(workspace["bindings"])
	bindings := array(workspace["bindings"])
	id := textField(args, "binding_id", 64, name != "upsert_binding")
	index := -1
	for i, raw := range bindings {
		if object(raw)["id"] == id {
			index = i
			break
		}
	}
	if id != "" && index < 0 {
		fail(404, "当前工作区不存在此场景实例，请使用 read_bindings 返回的 binding_id")
	}
	var binding Doc
	if index >= 0 {
		binding = object(bindings[index])
	}
	switch name {
	case "set_binding_profile":
		// Empty rule profile means inheritance; empty default means unbound.
		if _, present := args["profile_id"]; !present {
			fail(400, "必须指定 profile_id；空字符串表示清除绑定")
		}
		profileID := textField(args, "profile_id", 64, false)
		ruleID := textField(args, "rule_id", 120, false)
		if ruleID == "" {
			binding["profile_id"] = profileID
		} else {
			if binding["rule_profiles"] == nil {
				binding["rule_profiles"] = Doc{}
			}
			object(binding["rule_profiles"])[ruleID] = profileID
		}
	case "upsert_binding":
		patch, err := parseAgentJSON(textField(args, "json", 64000, true))
		if err != nil {
			return nil, err
		}
		for key := range patch {
			if !has([]any{"scenario_id", "profile_id", "enabled", "paths", "rule_profiles", "site_node_id"}, key) {
				fail(400, "不支持的绑定字段："+key)
			}
		}
		if binding == nil {
			id = randomHex(6)
			binding = Doc{"id": id, "enabled": true, "profile_id": "", "paths": Doc{}, "rule_profiles": Doc{}}
			bindings = append(bindings, binding)
		}
		if _, present := patch["scenario_id"]; present {
			scenario := get(a.manager.store.db, "scenarios", textField(patch, "scenario_id", 64, true))
			valid := map[string]bool{}
			for _, raw := range array(scenario["rules"]) {
				valid[str(object(raw)["id"])] = true
			}
			// A scenario replacement retains overrides only for surviving rule IDs.
			for _, field := range []string{"paths", "rule_profiles"} {
				for key := range object(binding[field]) {
					if !valid[key] {
						delete(object(binding[field]), key)
					}
				}
			}
		}
		for key, value := range patch {
			if key == "paths" || key == "rule_profiles" {
				entries := object(value)
				if entries == nil {
					fail(400, key+" 必须为规则 ID 到字符串的对象；空字符串清除该项覆盖")
				}
				if binding[key] == nil {
					binding[key] = Doc{}
				}
				merge(object(binding[key]), entries)
			} else {
				binding[key] = value
			}
		}
	case "delete_binding":
		bindings = append(bindings[:index], bindings[index+1:]...)
	case "move_binding":
		position, ok := number(args["position"])
		if !ok || position < 1 || position > int64(len(bindings)) {
			fail(400, "position 必须为 1 到实例总数的整数")
		}
		bindings = append(bindings[:index], bindings[index+1:]...)
		to := int(position) - 1
		bindings = append(bindings, nil)
		copy(bindings[to+1:], bindings[to:])
		bindings[to] = binding
	default:
		fail(400, "未知绑定操作")
	}
	workspace["bindings"] = bindings
	if dump(bindings) == before {
		return merge(a.bindingState(workspace), Doc{"ok": true, "changed": false, "binding_id": id, "persistence": a.persistenceState()}), nil
	}
	// saveComposer performs version checking, validates references/routes and
	// atomically republishes existing deployments, preserving history.
	saved := a.manager.store.saveComposer("workspaces", workspace)
	a.bindingVersion = integer(saved["version"])
	a.recordMaterialAction("saved", "workspace", saved)
	return merge(a.bindingState(saved), Doc{"ok": true, "changed": true, "binding_id": id, "live_update": "绑定已保存，已发布端口自动同步，无需重复保存或发布。", "persistence": a.persistenceState()}), nil
}

func generationBindingTools() []Doc {
	s := func(description string) Doc { return Doc{"type": "string", "description": description} }
	version := Doc{"type": "integer", "minimum": 1, "description": "Latest workspace version from read_bindings or a successful binding write"}
	return []Doc{
		generationTool("read_bindings", "Read CURRENT saved workspace scenario instances, IDs, order, mapped paths, default profiles and effective per-rule profiles. No response/prompt bodies. Read before changing bindings. Browser-local unsaved changes are not included.", Doc{}),
		generationTool("set_binding_profile", "Switch a CURRENT workspace instance's default prompt, or one rule's prompt when rule_id is supplied. Empty profile_id clears the default or restores rule inheritance. Existing rule overrides are preserved when changing the default. Use list_materials(profile) to resolve exact names. Saves and hot-updates published ports; never edits prompt text or other workspaces.", Doc{"version": version, "binding_id": s("Exact instance ID from read_bindings"), "rule_id": s("Optional rule ID; omit to change the instance default"), "profile_id": s("Exact saved profile ID, or empty string to clear/inherit")}, "version", "binding_id", "profile_id"),
		generationTool("upsert_binding", "Add or patch ONE current workspace scenario instance. Omit binding_id only to add. json fields: scenario_id (required when adding), profile_id, enabled, paths and rule_profiles and site_node_id. Map fields PATCH individual rule IDs; empty string clears that override. Replacing scenario preserves default profile and only overrides for surviving rule IDs. No other fields allowed. Saves immediately; does not create/delete shared materials or publish new workspaces.", Doc{"version": version, "binding_id": s("Existing instance ID; omit to add a requested new instance"), "json": s("JSON field patch")}, "version", "json"),
		generationTool("delete_binding", "Remove ONLY the explicitly requested scenario instance from the CURRENT workspace. Never deletes its saved scenario or profile. Read bindings first. Saves immediately and updates published ports.", Doc{"version": version, "binding_id": s("Exact instance ID")}, "version", "binding_id"),
		generationTool("move_binding", "Move one CURRENT workspace instance to a 1-based position to change request matching priority, preserving all instance settings. Read bindings first. Saves immediately and updates published ports.", Doc{"version": version, "binding_id": s("Exact instance ID"), "position": Doc{"type": "integer", "minimum": 1}}, "version", "binding_id", "position"),
	}
}
