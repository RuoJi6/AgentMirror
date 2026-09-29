package lab

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func TestSavedProfileRebindingUpdatesExistingSession(t *testing.T) {
	b := newLab(t)
	site, scenario, workspace := composerFixture(b)
	profileA := get(b.app.store.db, "profiles", str(object(array(workspace["bindings"])[0])["profile_id"]))
	profileB := b.api("/profiles", "POST", Doc{"name": "Replacement", "body": "REBOUND_B {{run_id}} {{token}}", "category": "unexpected_output"}, 200)
	// Identical scenario and rule IDs in another instance must not be rebound.
	workspace["bindings"] = append(array(workspace["bindings"]), Doc{"id": "second-instance", "scenario_id": scenario["id"], "profile_id": profileA["id"], "enabled": true, "paths": Doc{"download": "/second"}})
	workspace = b.api("/workspaces", "POST", workspace, 200)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	l := b.listener(deployment)
	base := str(l["public_url"])
	first := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	run := first.header.Get("X-Run-ID")
	history := b.app.store.session(run)
	snapshot := dump(history["snapshot"])
	release := dump(get(b.app.store.db, "composer_releases", str(deployment["release_id"])))
	oldEvents := array(history["events"])
	stale := clone(workspace)
	// Saves update site, scenario and prompt assignments on the same port/session.
	object(array(site["files"])[0])["content"] = "<title>NEW_PUBLICATION_SITE</title>"
	b.api("/sites", "POST", site, 200)
	object(array(scenario["rules"])[0])["path"] = "/new-route"
	b.api("/scenarios", "POST", scenario, 200)
	object(array(workspace["bindings"])[0])["profile_id"] = profileB["id"]
	workspace = b.api("/workspaces", "POST", workspace, 200)
	wantSaved := "REBOUND_B " + run + " "
	if r := b.request(base, "/new-route?file=notes.txt", "GET", nil); !strings.HasPrefix(str(r.doc()["notes"]), wantSaved) || r.header.Get("X-Run-ID") != run {
		t.Fatal("saved binding did not update the existing cookie session")
	}
	if get(b.app.store.db, "deployments", str(deployment["id"]))["release_id"] == deployment["release_id"] {
		t.Fatal("saving a binding did not create a new release")
	}
	if versions := array(b.app.store.sessionView(run, 1)["current_profiles"]); object(versions[0])["id"] != profileB["id"] {
		t.Fatal("saved binding not shown as current profile")
	}
	b.api("/workspaces", "POST", stale, 409)
	jar := b.client.Jar
	b.client.Jar, _ = cookiejar.New(nil)
	if r := b.request(base, "/new-route?file=notes.txt", "GET", nil); !strings.Contains(string(r.body), "REBOUND_B") || r.header.Get("X-Run-ID") == run {
		t.Fatal("new session did not use saved binding")
	}
	if r := b.request(base, "/", "GET", nil); !strings.Contains(string(r.body), "NEW_PUBLICATION_SITE") {
		t.Fatal("saved site not live")
	}
	if r := b.request(base, "/new-route?file=notes.txt", "GET", nil); r.status != 200 {
		t.Fatal("saved route not live")
	}
	b.client.Jar = jar
	b.restart()
	if r := b.request(base, "/new-route?file=notes.txt&run_id="+run, "GET", nil); !strings.HasPrefix(str(r.doc()["notes"]), wantSaved) {
		t.Fatal("restart lost saved binding or rejected stale save changed it")
	}
	// Publishing again and then changing a profile must keep the same run usable.
	b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	profileB["body"] = "REBOUND_B_V2 {{run_id}} {{token}}"
	profileB = b.api("/profiles", "POST", profileB, 200)
	want := "REBOUND_B_V2 " + run + " "
	for _, path := range []string{"/new-route?file=notes.txt", "/new-route?file=notes.txt&run_id=" + run} {
		r := b.request(base, path, "GET", nil)
		if r.status != 200 || !strings.HasPrefix(str(r.doc()["notes"]), want) || r.header.Get("X-Run-ID") != run {
			t.Fatal("existing session still used previous profile", r.status)
		}
	}
	req, _ := http.NewRequest("GET", base+"/new-route?file=notes.txt", nil)
	req.Header.Set("X-Run-ID", run)
	r, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(body), "REBOUND_B_V2") {
		t.Fatal("header session did not rebind")
	}
	if r := b.request(base, "/second?file=notes.txt", "GET", nil); string(r.body) != string(first.body) {
		t.Fatal("rebound another scenario instance")
	}
	if r := b.request(base, "/", "GET", nil); !strings.Contains(string(r.body), "NEW_PUBLICATION_SITE") {
		t.Fatal("saved site not live after rebind")
	}
	if r := b.request(base, "/new-route?file=notes.txt", "GET", nil); r.status != 200 {
		t.Fatal("saved route not live after rebind")
	}
	metadata := array(b.app.store.sessionView(run, 1)["current_profiles"])
	if len(metadata) != 2 || object(metadata[0])["id"] != profileB["id"] || integer(object(metadata[0])["version"]) != 2 || object(metadata[1])["id"] != profileA["id"] {
		t.Fatal("current version view did not follow binding")
	}
	after := b.app.store.session(run)
	if dump(after["snapshot"]) != snapshot || dump(get(b.app.store.db, "composer_releases", str(deployment["release_id"]))) != release {
		t.Fatal("rebind rewrote historical snapshots")
	}
	for i, e := range oldEvents {
		if dump(e) != dump(array(after["events"])[i]) {
			t.Fatal("rebind rewrote old events")
		}
	}
	found := false
	for _, raw := range array(after["events"]) {
		e := object(raw)
		detail := object(e["detail"])
		for _, d := range array(detail["deliveries"]) {
			if object(d)["profile_id"] == profileB["id"] && integer(object(d)["profile_version"]) == 2 && strings.Contains(str(detail["body"]), "REBOUND_B_V2") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("new delivery did not record actual replacement profile")
	}
	jar = b.client.Jar
	b.client.Jar, _ = cookiejar.New(nil)
	newRead := b.request(base, "/new-route?file=notes.txt", "GET", nil)
	if !strings.Contains(string(newRead.body), "REBOUND_B_V2") || newRead.header.Get("X-Run-ID") == run {
		t.Fatal("new run did not use new release")
	}
	b.client.Jar = jar
	b.restart()
	if r := b.request(base, "/new-route?file=notes.txt&run_id="+run, "GET", nil); !strings.HasPrefix(str(r.doc()["notes"]), want) {
		t.Fatal("restart lost published binding resolution")
	}
	if r := b.request(base, "/collect", "POST", Doc{"run_id": run, "token": object(history["snapshot"])["token"], "data": "AFTER_REBIND"}); r.status != 403 {
		t.Fatal("later requests left the original receipt token valid")
	}
}

func TestLivePromptBindingIsScopedToScenarioInstance(t *testing.T) {
	b := newLab(t)
	a := Doc{"id": "a", "body": "original"}
	binding := Doc{"binding_id": "instance", "scenario_id": "scenario-a", "profile": a}
	b.app.store.write(func(q queryer) {
		put(q, "deployments", Doc{"id": "deployment", "mode": "composable", "workspace_id": "workspace", "release_id": "release"})
		put(q, "composer_releases", Doc{"id": "release", "rules": []any{Doc{"binding_id": "instance", "scenario_id": "scenario-b", "profile": Doc{"id": "b"}}, Doc{"binding_id": "other", "scenario_id": "scenario-a", "profile": Doc{"id": "c"}}}})
		snap := Doc{"deployment": Doc{"id": "deployment", "workspace_id": "workspace", "mode": "composable"}}
		put(q, "profiles", Doc{"id": "saved", "body": "saved replacement"})
		put(q, "workspaces", Doc{"id": "workspace", "deployment_id": "deployment", "bindings": []any{
			Doc{"id": "instance", "scenario_id": "scenario-a", "profile_id": "saved"},
			Doc{"id": "other", "scenario_id": "scenario-a", "profile_id": "saved"},
		}})
		if boundPrompt(binding, livePromptBindings(q, snap))["id"] != "a" {
			t.Fatal("matched different instance or replaced scenario")
		}
		other := Doc{"binding_id": "other", "scenario_id": "scenario-a"}
		if boundPrompt(other, livePromptBindings(q, snap))["id"] != "saved" {
			t.Fatal("saved binding did not override published profile")
		}
		workspace := get(q, "workspaces", "workspace")
		workspace["deployment_id"] = "unrelated"
		put(q, "workspaces", workspace)
		if boundPrompt(other, livePromptBindings(q, snap))["id"] != "c" {
			t.Fatal("read saved bindings from an unrelated deployment")
		}
		snap["deployment"] = Doc{"id": "deployment", "workspace_id": "other-workspace", "mode": "composable"}
		if len(livePromptBindings(q, snap)) != 0 {
			t.Fatal("cross-workspace binding lookup")
		}
	})
}
