package lab

import (
	evaluated "agentmirror/examples/evaluated-scenarios"
	"io/fs"
	"path"
	"path/filepath"
	"testing"
)

func TestBuiltinScenariosFreshAndRestart(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "builtin.sqlite3")
	s, err := openStore(filename, "http://localhost:8765")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.db.Close() }()
	for _, kind := range []string{"profiles", "sites", "scenarios", "workspaces"} {
		if got := len(listing(s.db, kind)); got != 12 {
			t.Fatalf("%s: got %d, want 12", kind, got)
		}
	}
	for _, kind := range []string{"deployments", "listeners", "composer_releases", "hosted_files"} {
		if len(listing(s.db, kind)) != 0 {
			t.Fatalf("unexpected activation or attachment: %s", kind)
		}
	}
	for _, workspace := range workspaceListing(s.db) {
		if workspace["publication_status"] != "draft" {
			t.Fatal("builtin is not a draft")
		}
		if len(compileWorkspaceBindings(s.db, workspace)) < 3 {
			t.Fatal("rules missing")
		}
		site := get(s.db, "sites", str(workspace["site_id"]))
		validateSite(site)
		for _, value := range array(site["files"]) {
			if object(value)["encoding"] == "hosted" {
				t.Fatal("attachment embedded")
			}
		}
	}
	// Check binding fidelity against every frozen source, not just row counts.
	mapping := object(get(s.db, "builtin_seeds", evaluatedSeedID)["workspaces"])
	names, err := fs.Glob(evaluated.Files, "workspaces/*/workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := evaluated.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		bundle := decode(string(data))
		workspace := get(s.db, "workspaces", str(mapping[path.Base(path.Dir(name))]))
		if workspace["name"] != bundle["name"] {
			t.Fatal("wrong workspace name")
		}
		compiled := compileWorkspaceBindings(s.db, workspace)
		if len(compiled) != len(array(bundle["rules"])) {
			t.Fatal("lost frozen rules")
		}
		for index, value := range array(bundle["rules"]) {
			expected := object(value)
			actual := compiled[index]
			for _, key := range []string{"id", "method", "path", "conditions", "delivery_required"} {
				if dump(actual[key]) != dump(expected[key]) {
					t.Fatalf("changed %s in %s", key, name)
				}
			}
			if dump(actual["response"]) != dump(validateScenarioResponse(object(expected["response"]), boolean(expected["delivery_required"]))) {
				t.Fatal("changed response")
			}
			if ref := str(expected["profile_ref"]); ref != "" {
				data, err := evaluated.Files.ReadFile(path.Join("prompts", path.Base(ref)))
				if err != nil {
					t.Fatal(err)
				}
				profile := decode(string(data))
				for _, key := range []string{"name", "body", "category", "fields", "commands"} {
					if dump(object(actual["profile"])[key]) != dump(profile[key]) {
						t.Fatalf("wrong prompt %s in %s", key, name)
					}
				}
			}
		}
	}
	original := listing(s.db, "workspaces")[0]
	s.renameWorkspace(str(original["id"]), Doc{"name": "User renamed", "version": original["version"]})
	removed := str(listing(s.db, "workspaces")[1]["id"])
	s.write(func(q queryer) { exec(q, "DELETE FROM entities WHERE kind='workspaces' AND id=?", removed) })
	s.db.Close()
	s, err = openStore(filename, "http://localhost:8765")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing(s.db, "workspaces")) != 11 || len(listing(s.db, "profiles")) != 12 {
		t.Fatal("restart duplicated or resurrected builtins")
	}
	if get(s.db, "workspaces", str(original["id"]))["name"] != "User renamed" {
		t.Fatal("user edit overwritten")
	}
}

func TestBuiltinScenariosUpgradePreservesEditsReferencesAndHistory(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "upgrade.sqlite3")
	s, err := openStore(filename, "http://localhost:8765")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.db.Close() }()
	resetAuthoringTestFixtures(s)
	// Reproduce a 0.0.1 database: no migration marker, four old defaults.
	s.write(func(q queryer) {
		exec(q, "DELETE FROM entities WHERE kind='builtin_seeds'")
		custom := get(q, "profiles", "custom")
		custom["body"] = "User authored text"
		put(q, "profiles", custom)
		put(q, "workspaces", Doc{"id": "user-workspace", "name": "User workspace", "slug": "release-desk-integrity", "bindings": []any{Doc{"profile_id": "receipt"}}})
		exec(q, "INSERT INTO sessions(id, deployment_id, snapshot) VALUES(?,?,?)", "old-session", "old", dump(Doc{"profile": Doc{"id": "environment"}}))
	})
	s.db.Close()
	s, err = openStore(filename, "http://localhost:8765")
	if err != nil {
		t.Fatal(err)
	}
	if entityMaybe(s.db, "profiles", "observe") != nil {
		t.Fatal("unused legacy default retained")
	}
	for _, id := range []string{"custom", "receipt", "environment"} {
		if entityMaybe(s.db, "profiles", id) == nil {
			t.Fatalf("deleted edited/referenced/historical profile %s", id)
		}
	}
	if get(s.db, "profiles", "custom")["body"] != "User authored text" {
		t.Fatal("edited profile overwritten")
	}
	if len(listing(s.db, "workspaces")) != 13 {
		t.Fatal("upgrade lost a workspace")
	}
	slugs := map[string]bool{}
	for _, w := range listing(s.db, "workspaces") {
		slug := str(w["slug"])
		if slugs[slug] {
			t.Fatal("slug collision")
		}
		slugs[slug] = true
	}
	if len(listing(s.db, "listeners")) != 0 || len(listing(s.db, "deployments")) != 0 {
		t.Fatal("upgrade auto-published")
	}
}

// Isolate the legacy authoring contracts from product seed data. Production
// initialization is exercised above and by the cross-platform standalone test.
func resetAuthoringTestFixtures(s *Store) {
	s.write(func(q queryer) {
		exec(q, "DELETE FROM entities WHERE kind IN ('profiles','sites','sites_versions','scenarios','scenarios_versions','workspaces')")
		exec(q, "DELETE FROM versions")
		for _, value := range array(defaults["profiles"]) {
			doc := clone(object(value))
			merge(doc, Doc{"version": 1, "created_at": timestamp(), "updated_at": timestamp()})
			put(q, "profiles", doc)
			exec(q, "INSERT INTO versions VALUES(?,?,?)", doc["id"], 1, dump(doc))
		}
	})
}
