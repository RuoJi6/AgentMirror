package lab

import (
	evaluated "agentmirror/examples/evaluated-scenarios"
	"io/fs"
	"path"
	"strings"
)

const evaluatedSeedID = "evaluated-scenarios-v1"

// Seed once, atomically, including upgrades from 0.0.1. The marker survives
// user deletions so a restart never resurrects a removed example.
func (s *Store) seedEvaluatedScenarios() {
	s.write(func(q queryer) {
		if entityMaybe(q, "builtin_seeds", evaluatedSeedID) != nil {
			return
		}
		names, err := fs.Glob(evaluated.Files, "workspaces/*/workspace.json")
		check(err)
		profiles := map[string]string{}
		imported := Doc{}
		for _, name := range names {
			data, err := evaluated.Files.ReadFile(name)
			check(err)
			bundle := decode(string(data))
			sourceID := path.Base(path.Dir(name))
			// Existing evaluation installations already own these workspace IDs.
			// Never replace or duplicate their authored material.
			if existing := entityMaybe(q, "workspaces", sourceID); existing != nil {
				imported[sourceID] = existing["id"]
				continue
			}
			rules := []any{}
			ruleProfiles := Doc{}
			for _, value := range array(bundle["rules"]) {
				rule := clone(object(value))
				ref := str(rule["profile_ref"])
				if ref != "" {
					filename := path.Join("prompts", path.Base(ref))
					id := profiles[filename]
					if id == "" {
						data, err := evaluated.Files.ReadFile(filename)
						check(err)
						profile := decode(string(data))
						profile["fields"], profile["commands"] = validateSpec(profile)
						id = randomHex(6)
						merge(profile, Doc{"id": id, "version": 1, "created_at": timestamp(), "updated_at": timestamp(), "revision_origin": Doc{"kind": "builtin"}})
						put(q, "profiles", profile)
						exec(q, "INSERT INTO versions VALUES(?,?,?)", id, 1, dump(profile))
						profiles[filename] = id
					}
					ruleProfiles[str(rule["id"])] = id
				}
				for _, key := range []string{"profile_ref", "scenario_id", "scenario_name", "scenario_version", "binding_id"} {
					delete(rule, key)
				}
				rules = append(rules, rule)
			}
			site := saveComposerIn(q, "sites", clone(object(bundle["site"])))
			description := "内置已测场景；按需配置并发布。"
			for _, value := range array(bundle["excluded_hosted_files"]) {
				description += " 未包含下载附件：" + str(object(value)["path"]) + "。"
			}
			scenario := saveComposerIn(q, "scenarios", Doc{"name": bundle["name"], "description": description, "rules": rules})
			slug := str(object(bundle["workspace"])["slug"])
			used := map[string]bool{}
			for _, existing := range listing(q, "workspaces") {
				used[str(existing["slug"])] = true
			}
			if used[slug] {
				slug = "builtin-" + sourceID
			}
			for used[slug] {
				slug = "builtin-" + randomHex(6)
			}
			raw := clone(object(bundle["settings"]))
			merge(raw, Doc{"name": bundle["name"], "slug": slug, "site_id": site["id"], "bindings": []any{Doc{"scenario_id": scenario["id"], "rule_profiles": ruleProfiles, "enabled": true}}})
			workspace := saveComposerIn(q, "workspaces", raw)
			imported[sourceID] = workspace["id"]
		}
		removeUnusedLegacyProfiles(q)
		put(q, "builtin_seeds", Doc{"id": evaluatedSeedID, "workspaces": imported, "created_at": timestamp()})
	})
}

// Be conservative with upgraded data: only the exact original, single-version
// defaults are eligible. Retain anything edited, referenced or used in history.
func removeUnusedLegacyProfiles(q queryer) {
	for _, value := range array(defaults["profiles"]) {
		original := object(value)
		id := str(original["id"])
		current := entityMaybe(q, "profiles", id)
		if current == nil || integer(current["version"]) != 1 || count(q, "SELECT COUNT(*) FROM versions WHERE profile_id=?", id) != 1 {
			continue
		}
		comparable := clone(current)
		for _, key := range []string{"version", "created_at", "updated_at"} {
			delete(comparable, key)
		}
		if dump(comparable) != dump(original) {
			continue
		}
		needle := "\"" + id + "\""
		referenced := false
		for _, kind := range []string{"workspaces", "deployments", "listeners", "composer_releases"} {
			for _, doc := range listing(q, kind) {
				if strings.Contains(dump(doc), needle) {
					referenced = true
					break
				}
			}
			if referenced {
				break
			}
		}
		if referenced || count(q, "SELECT COUNT(*) FROM sessions WHERE instr(snapshot, ?) > 0", needle) > 0 {
			continue
		}
		exec(q, "DELETE FROM entities WHERE kind='profiles' AND id=?", id)
		exec(q, "DELETE FROM versions WHERE profile_id=?", id)
	}
}
