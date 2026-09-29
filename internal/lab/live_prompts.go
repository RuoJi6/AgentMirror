package lab

// Requests follow the saved profile binding of a published instance, then that
// profile's latest version. Releases, session snapshots and past events stay immutable.
type liveProfiles struct {
	q     queryer
	cache map[string]Doc
}

func (p *liveProfiles) resolve(snapshot Doc) Doc {
	id := str(snapshot["id"])
	if id == "" {
		return snapshot
	}
	if p.cache == nil {
		p.cache = map[string]Doc{}
	}
	latest, loaded := p.cache[id]
	if !loaded {
		latest = entityMaybe(p.q, "profiles", id)
		p.cache[id] = latest
	}
	if latest != nil {
		return latest
	}
	// A historical binding may outlive a removed profile. Its saved copy is
	// still usable; never silently switch it to a different profile.
	return snapshot
}

// Resolve assignments by stable instance/rule identity. Current requests refresh
// their release first; historical snapshots remain untouched.
func livePromptBindings(q queryer, snap Doc) map[string]Doc {
	bindings := map[string]Doc{}
	previous := object(snap["deployment"])
	deployment := entityMaybe(q, "deployments", str(previous["id"]))
	if deployment == nil || deployment["mode"] != "composable" || deployment["workspace_id"] != previous["workspace_id"] {
		return bindings
	}
	workspace := entityMaybe(q, "workspaces", str(deployment["workspace_id"]))
	saved := map[string]Doc{}
	if workspace != nil && workspace["deployment_id"] == deployment["id"] {
		for _, raw := range array(workspace["bindings"]) {
			binding := object(raw)
			saved[scenarioInstanceKey(binding["id"], binding["scenario_id"])] = binding
		}
	}
	profiles := liveProfiles{q: q}
	release := entityMaybe(q, "composer_releases", str(deployment["release_id"]))
	for _, raw := range array(release["rules"]) {
		rule := object(raw)
		key := promptBindingKey(rule)
		if profile := object(rule["profile"]); str(profile["id"]) != "" {
			bindings[key] = profile
		}
		binding := saved[scenarioInstanceKey(rule["binding_id"], rule["scenario_id"])]
		id := bindingProfileID(binding, str(rule["id"]))
		if id == "" {
			continue
		}
		profile := profiles.resolve(Doc{"id": id})
		if profiles.cache[id] != nil {
			bindings[key] = profile
		}
	}
	return bindings
}

func promptBindingKey(rule Doc) string {
	return scenarioInstanceKey(rule["binding_id"], rule["scenario_id"]) + "\x00" + str(rule["id"])
}

func scenarioInstanceKey(bindingID, scenarioID any) string {
	return str(bindingID) + "\x00" + str(scenarioID)
}

func boundPrompt(rule Doc, bindings map[string]Doc) Doc {
	if current := bindings[promptBindingKey(rule)]; current != nil {
		return current
	}
	return object(rule["profile"])
}

func refreshSessionPrompts(q queryer, session Doc) {
	snap := object(session["snapshot"])
	profiles := liveProfiles{q: q}
	composed := object(snap["deployment"])["mode"] == "composable"
	if composed {
		bindings := livePromptBindings(q, snap)
		primary := Doc(nil)
		for _, raw := range array(snap["rules"]) {
			rule := object(raw)
			if bound := boundPrompt(rule, bindings); bound != nil {
				rule["profile"] = profiles.resolve(bound)
				if primary == nil {
					primary = object(rule["profile"])
				}
			}
		}
		snap["profile"] = primary
	} else {
		snap["profile"] = profiles.resolve(object(snap["profile"]))
	}
	context := Doc{"run_id": session["id"], "token": snap["token"], "callback_url": snap["callback_url"]}
	if composed {
		snap["instruction"] = scenarioInstruction(object(snap["profile"]), context)
	} else {
		snap["instruction"] = renderInstruction(object(snap["profile"]), context)
	}
}

func currentPromptVersions(q queryer, snap Doc) []Doc {
	profiles := liveProfiles{q: q}
	candidates := []Doc{object(snap["profile"])}
	if object(snap["deployment"])["mode"] == "composable" {
		current := clone(snap)
		deployment := entityMaybe(q, "deployments", str(object(snap["deployment"])["id"]))
		if deployment != nil && deployment["workspace_id"] == object(snap["deployment"])["workspace_id"] {
			release := entityMaybe(q, "composer_releases", str(deployment["release_id"]))
			if release != nil {
				listener := object(snap["listener"])
				found := listenerSiteID(listener) == workspaceRootSite
				for _, raw := range array(release["site_mounts"]) {
					found = found || object(raw)["id"] == listenerSiteID(listener)
				}
				if found {
					current["rules"] = listenerSiteRelease(release, listener)["rules"]
				}
			}
		}
		bindings := livePromptBindings(q, current)
		candidates = nil
		for _, raw := range array(current["rules"]) {
			candidates = append(candidates, boundPrompt(object(raw), bindings))
		}
	}
	result, seen := []Doc{}, map[string]bool{}
	for _, snapshot := range candidates {
		id := str(snapshot["id"])
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		profile := profiles.resolve(snapshot)
		result = append(result, Doc{"id": id, "name": profile["name"], "version": profile["version"], "available": profiles.cache[id] != nil})
	}
	return result
}

func (s *Store) recordLegacyPrompt(session Doc, kind string, detail Doc) {
	if kind == "delivery" {
		snap := object(session["snapshot"])
		profile := object(snap["profile"])
		merge(detail, Doc{"instruction": snap["instruction"], "deliveries": []any{Doc{"profile_id": profile["id"], "profile_name": profile["name"], "profile_version": profile["version"]}}})
	}
	s.record(str(session["id"]), kind, detail)
}
