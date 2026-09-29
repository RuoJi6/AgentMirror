package lab

import (
	"strings"
	"testing"
)

func TestWorkspaceRenameOnlyUpdatesIdentity(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	id := str(workspace["id"])
	deployment := b.api("/workspaces/"+id+"/publish", "POST", Doc{}, 200)
	workspace = b.api("/workspaces/"+id, "GET", nil, 200)
	listener := b.listener(deployment)
	before := b.request(str(listener["public_url"]), "/", "GET", nil)
	run := before.header.Get("X-Run-ID")
	snapshot := dump(b.app.store.session(run)["snapshot"])
	release := dump(get(b.app.store.db, "composer_releases", str(deployment["release_id"])))
	rename := "/workspaces/" + id + "/rename"
	updated := b.api(rename, "POST", Doc{"name": "新名称", "version": workspace["version"], "site_id": "ignored", "bindings": []any{}, "slug": "ignored"}, 200)
	if updated["name"] != "新名称" || integer(updated["version"]) != integer(workspace["version"])+1 {
		t.Fatal("rename did not advance identity version")
	}
	for _, key := range []string{"slug", "site_id", "bindings", "deployment_id", "published_version", "publication_revision"} {
		if dump(updated[key]) != dump(workspace[key]) {
			t.Fatalf("rename changed %s", key)
		}
	}
	afterDeployment := get(b.app.store.db, "deployments", str(deployment["id"]))
	if afterDeployment["name"] != "新名称" || afterDeployment["release_id"] != deployment["release_id"] || afterDeployment["slug"] != deployment["slug"] || afterDeployment["revision"] != deployment["revision"] {
		t.Fatal("deployment identity update changed routing or published revision")
	}
	b.api(rename, "POST", Doc{"name": "覆盖", "version": workspace["version"]}, 409)
	b.api(rename, "POST", Doc{"name": "缺版本"}, 400)
	b.api(rename, "POST", Doc{"name": " ", "version": updated["version"]}, 400)
	b.api(rename, "POST", Doc{"name": strings.Repeat("名", 81), "version": updated["version"]}, 400)
	unchanged := b.api(rename, "POST", Doc{"name": "新名称", "version": updated["version"]}, 200)
	if unchanged["version"] != updated["version"] {
		t.Fatal("unchanged name created a new revision")
	}
	if dump(b.app.store.session(run)["snapshot"]) != snapshot || dump(get(b.app.store.db, "composer_releases", str(deployment["release_id"]))) != release {
		t.Fatal("rename rewrote historical evidence")
	}
	after := b.request(str(listener["public_url"]), "/?run_id="+run, "GET", nil)
	expectStatus(t, after, 200)
	if string(after.body) != string(before.body) {
		t.Fatal("rename changed public response")
	}
}

func TestSiteDeletionChecksReferencesAndKeepsPublishedVersions(t *testing.T) {
	b := newLab(t)
	site, _, workspace := composerFixture(b)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	listener := b.listener(deployment)
	base := str(listener["public_url"])
	before := b.request(base, "/", "GET", nil)
	run := before.header.Get("X-Run-ID")
	path := "/sites/" + str(site["id"])
	b.api(path, "DELETE", nil, 409)
	workspace = b.api("/workspaces/"+str(workspace["id"]), "GET", nil, 200)
	workspace["site_id"] = ""
	b.api("/workspaces", "POST", workspace, 200)
	b.api(path, "DELETE", nil, 200)
	b.api(path, "GET", nil, 404)
	if entityMaybe(b.app.store.db, "sites_versions", versionKey(site)) == nil {
		t.Fatal("deleting material removed its immutable version")
	}
	for _, suffix := range []string{"/?run_id=" + run, "/"} {
		after := b.request(base, suffix, "GET", nil)
		expectStatus(t, after, 404)
		if string(after.body) == string(before.body) {
			t.Fatal("unlinked site still served")
		}
	}
}

func TestScenarioDeletionChecksReferencesAndKeepsPublishedVersions(t *testing.T) {
	b := newLab(t)
	_, scenario, workspace := composerFixture(b)
	// Use an ordinary response to isolate rule snapshot retention from live profile bindings.
	rule := object(array(scenario["rules"])[0])
	rule["delivery_required"] = false
	object(rule["response"])["body"] = `{"notes":"SYNTHETIC"}`
	scenario = b.api("/scenarios", "POST", scenario, 200)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	listener := b.listener(deployment)
	base := str(listener["public_url"])
	before := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	expectStatus(t, before, 200)
	run := before.header.Get("X-Run-ID")
	path := "/scenarios/" + str(scenario["id"])
	b.api(path, "DELETE", nil, 409)
	workspace = b.api("/workspaces/"+str(workspace["id"]), "GET", nil, 200)
	workspace["bindings"] = []any{}
	b.api("/workspaces", "POST", workspace, 200)
	b.api(path, "DELETE", nil, 200)
	b.api(path, "GET", nil, 404)
	if entityMaybe(b.app.store.db, "scenarios_versions", versionKey(scenario)) == nil {
		t.Fatal("immutable scenario version removed")
	}
	for _, suffix := range []string{"&run_id=" + run, ""} {
		after := b.request(base, "/api/download?file=notes.txt"+suffix, "GET", nil)
		expectStatus(t, after, 404)
		if string(after.body) == string(before.body) {
			t.Fatal("unlinked scenario still served")
		}
	}
}
