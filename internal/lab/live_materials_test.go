package lab

import (
	"strings"
	"testing"
)

func TestSavedReplacementUpdatesSameWorkspacePortAndSession(t *testing.T) {
	b := newLab(t)
	site, scenario, workspace := composerFixture(b)
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	listener := b.listener(dep)
	base := str(listener["public_url"])
	listenerBefore := dump(get(b.app.store.db, "listeners", str(listener["id"])))
	first := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	run := first.header.Get("X-Run-ID")
	history := b.app.store.session(run)
	oldRelease := dump(get(b.app.store.db, "composer_releases", str(dep["release_id"])))
	other := clone(scenario)
	delete(other, "id")
	delete(other, "version")
	rule := object(array(other["rules"])[0])
	rule["id"], rule["path"] = "replacement-rule", "/new/download"
	other = b.api("/scenarios", "POST", other, 200)
	profile := b.api("/profiles", "POST", Doc{"name": "New assignment", "body": "NEW_ASSIGNMENT {{run_id}}", "category": "unexpected_output"}, 200)
	workspace["bindings"] = []any{Doc{"id": "replacement-instance", "scenario_id": other["id"], "profile_id": profile["id"], "enabled": true}}
	workspace = b.api("/workspaces", "POST", workspace, 200)
	if workspace["published_version"] != workspace["version"] {
		t.Fatal("saved version is not live")
	}
	assertCurrent := func() {
		t.Helper()
		r := b.request(base, "/new/download?file=notes.txt&run_id="+run, "GET", nil)
		if r.status != 200 || !strings.Contains(string(r.body), "NEW_ASSIGNMENT "+run) || r.header.Get("X-Run-ID") != run {
			t.Fatal("replacement requires a new workspace or session")
		}
		expectStatus(t, b.request(base, "/api/download?file=notes.txt&run_id="+run, "GET", nil), 404)
	}
	assertCurrent()
	object(array(site["files"])[0])["content"] = "<h1>Saved site now live</h1>"
	b.api("/sites", "POST", site, 200)
	if r := b.request(base, "/?run_id="+run, "GET", nil); !strings.Contains(string(r.body), "Saved site now live") {
		t.Fatal("existing session kept stale site")
	}
	currentDep := get(b.app.store.db, "deployments", str(dep["id"]))
	currentRelease := get(b.app.store.db, "composer_releases", str(currentDep["release_id"]))
	after := b.app.store.session(run)
	if dump(after["snapshot"]) != dump(history["snapshot"]) || dump(get(b.app.store.db, "composer_releases", str(dep["release_id"]))) != oldRelease {
		t.Fatal("history was rewritten")
	}
	for i, event := range array(history["events"]) {
		if dump(event) != dump(array(after["events"])[i]) {
			t.Fatal("past event changed")
		}
	}
	last := object(array(after["events"])[len(array(after["events"]))-1])
	if object(last["detail"])["release_id"] != currentRelease["id"] {
		t.Fatal("response did not record actual release")
	}
	if dump(get(b.app.store.db, "listeners", str(listener["id"]))) != listenerBefore {
		t.Fatal("saving rebound listener")
	}
	// Emulate a pre-upgrade release pointer; restart must reconcile saved state.
	b.app.store.write(func(q queryer) { put(q, "deployments", dep) })
	b.restart()
	assertCurrent()
	reconciled := dump(get(b.app.store.db, "deployments", str(dep["id"])))
	b.restart()
	if dump(get(b.app.store.db, "deployments", str(dep["id"]))) != reconciled {
		t.Fatal("restart created redundant releases")
	}
	if dump(b.app.store.session(run)["snapshot"]) != dump(history["snapshot"]) {
		t.Fatal("upgrade rewrote original session")
	}
}

func TestSharedScenarioSaveIsAtomicAndPrunesRemovedOverrides(t *testing.T) {
	b := newLab(t)
	_, scenario, workspace := composerFixture(b)
	// A second ordinary rule has its own path override and prompt assignment.
	rule := Doc{"id": "ordinary", "method": "GET", "path": "/ordinary", "conditions": []any{}, "delivery_required": false, "response": Doc{"status": 200, "format": "text", "content_type": "text/plain", "body": "OK"}}
	scenario["rules"] = append(array(scenario["rules"]), rule)
	scenario = b.api("/scenarios", "POST", scenario, 200)
	binding := object(array(workspace["bindings"])[0])
	binding["paths"] = Doc{"ordinary": "/mapped"}
	binding["rule_profiles"] = Doc{"ordinary": binding["profile_id"]}
	workspace = b.api("/workspaces", "POST", workspace, 200)
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	second := clone(workspace)
	delete(second, "id")
	second["name"], second["slug"] = "Shared draft", "shared-draft"
	second = b.api("/workspaces", "POST", second, 200)
	b.api("/workspaces/"+str(workspace["id"])+"/status", "POST", Doc{"enabled": false}, 200)
	scenario["rules"] = array(scenario["rules"])[:1]
	scenario = b.api("/scenarios", "POST", scenario, 200)
	for _, w := range []Doc{workspace, second} {
		updated := get(b.app.store.db, "workspaces", str(w["id"]))
		if integer(updated["version"]) <= integer(w["version"]) {
			t.Fatal("pruning lost optimistic concurrency")
		}
		bound := object(array(updated["bindings"])[0])
		if len(object(bound["paths"])) != 0 || len(object(bound["rule_profiles"])) != 0 {
			t.Fatal("deleted rule left stale overrides")
		}
	}
	if boolean(get(b.app.store.db, "deployments", str(dep["id"]))["enabled"]) {
		t.Fatal("save resumed paused deployment")
	}
	if get(b.app.store.db, "workspaces", str(second["id"]))["deployment_id"] != nil {
		t.Fatal("save published a draft workspace")
	}
	// Duplicate routes are valid within a reusable source but invalid when a
	// workspace mapping collides. Failure must rollback the source and release.
	workspace = get(b.app.store.db, "workspaces", str(workspace["id"]))
	object(array(workspace["bindings"])[0])["paths"] = Doc{"download": "/future"}
	workspace = b.api("/workspaces", "POST", workspace, 200)
	beforeEntities := dump(listing(b.app.store.db, "composer_releases"))
	beforeScenario := dump(get(b.app.store.db, "scenarios", str(scenario["id"])))
	duplicate := clone(object(array(scenario["rules"])[0]))
	duplicate["id"], duplicate["path"] = "duplicate", "/future"
	scenario["rules"] = append(array(scenario["rules"]), duplicate)
	b.api("/scenarios", "POST", scenario, 409)
	if dump(listing(b.app.store.db, "composer_releases")) != beforeEntities || dump(get(b.app.store.db, "scenarios", str(scenario["id"]))) != beforeScenario {
		t.Fatal("invalid shared save partially committed")
	}
}
