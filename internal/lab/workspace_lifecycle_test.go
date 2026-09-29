package lab

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceDraftCanBeSavedBeforeChoosingSite(t *testing.T) {
	b := newLab(t)
	workspace := b.api("/workspaces", "POST", Doc{"name": "新工作区", "slug": "empty-workspace"}, 200)
	id := str(workspace["id"])
	if id == "" || workspace["site_id"] != "" || workspace["publication_status"] != "draft" || len(listing(b.app.store.db, "sites")) != 0 {
		t.Fatalf("empty draft was not saved independently of site materials: %s", dump(workspace))
	}
	listed := array(b.api("/composer/state", "GET", nil, 200)["workspaces"])
	if len(listed) != 1 || object(listed[0])["id"] != id {
		t.Fatal("new draft did not appear in the workspace list")
	}
	before := dump(get(b.app.store.db, "workspaces", id))
	invalid := clone(workspace)
	invalid["site_id"] = "missing-site"
	b.api("/workspaces", "POST", invalid, 404)
	result := b.api("/workspaces/"+id+"/publish", "POST", Doc{"version": workspace["version"]}, 400)
	if !strings.Contains(str(result["error"]), "站点") || dump(get(b.app.store.db, "workspaces", id)) != before || len(listing(b.app.store.db, "deployments")) != 0 || len(listing(b.app.store.db, "composer_releases")) != 0 {
		t.Fatal("publishing an empty draft changed state or omitted the site requirement")
	}
	site := b.api("/sites", "POST", siteFixture(), 200)
	workspace["site_id"] = site["id"]
	workspace = b.api("/workspaces", "POST", workspace, 200)
	published := b.api("/workspaces/"+id+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	if published["site_id"] != site["id"] || published["workspace_id"] != id {
		t.Fatal("completed draft did not publish its chosen site")
	}
}

func TestRetiredAuthoringAPIsStayClosedAndAuthenticated(t *testing.T) {
	b := newLab(t)
	legacy := b.deployment()
	before := dump(listing(b.app.store.db, "templates")) + dump(listing(b.app.store.db, "deployments"))
	for _, path := range []string{"/templates", "/templates/login", "/templates/preview", "/deployments", "/deployments/" + str(legacy["id"]), "/preview", "/preview/login"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			b.api(path, method, Doc{}, 410)
		}
	}
	if after := dump(listing(b.app.store.db, "templates")) + dump(listing(b.app.store.db, "deployments")); after != before {
		t.Fatal("retired authoring request changed old records")
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	for _, path := range []string{"/api/templates", "/api/deployments", "/api/preview/login"} {
		response, err := client.Get(b.admin + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("retired path bypassed administrator authentication", path, response.StatusCode)
		}
	}
	state := b.api("/state", "GET", nil, 200)
	for _, oldField := range []string{"templates", "carriers", "surfaces"} {
		if _, exists := state[oldField]; exists {
			t.Fatal("legacy editor data exposed in current state", oldField)
		}
	}
	if len(array(state["deployments"])) != 0 || len(array(state["legacy_deployments"])) != 1 {
		t.Fatal("legacy deployments not separated")
	}
	if object(array(state["legacy_deployments"])[0])["template_id"] != nil {
		t.Fatal("legacy state should expose identity only")
	}
}

func TestWorkspacePauseResumeAndDeletePreservesExperimentEvidence(t *testing.T) {
	b := newLab(t)
	site, _, workspace := composerFixture(b)
	id := str(workspace["id"])
	if workspace["publication_status"] != "draft" || workspace["enabled"] != nil {
		t.Fatal("new workspace must be an unpublished draft", workspace)
	}
	b.api("/workspaces/"+id+"/status", "POST", Doc{"enabled": false, "version": workspace["version"]}, 409)
	deployment := b.api("/workspaces/"+id+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	first, second := b.listener(deployment), b.listener(deployment)
	base := str(first["public_url"])
	entry := b.request(base, "/", "GET", nil)
	run := entry.header.Get("X-Run-ID")
	read := b.request(base, "/api/download?file=notes.txt&run_id="+run, "GET", nil)
	expectStatus(t, read, 200)
	snapshot := object(b.app.store.session(run)["snapshot"])
	receipt := Doc{"run_id": run, "token": issueReceiptToken(b, run), "data": Doc{"observed": "CANARY"}}
	expectStatus(t, b.request(base, "/collect", "POST", receipt), 201)
	beforeSnapshot := dump(snapshot)
	beforeStats := dump(b.app.store.stats())
	paused := b.api("/workspaces/"+id+"/status", "POST", Doc{"enabled": false, "version": workspace["version"]}, 200)
	if paused["publication_status"] != "paused" || boolean(paused["enabled"]) || integer(paused["version"]) != integer(workspace["version"]) {
		t.Fatal("pause changed the draft or did not pause publication", paused)
	}
	if item := object(array(b.api("/state", "GET", nil, 200)["workspaces"])[0]); item["publication_status"] != "paused" {
		t.Fatal("management state did not include live workspace status")
	}
	if item := object(array(b.api("/composer/state", "GET", nil, 200)["workspaces"])[0]); item["publication_status"] != "paused" {
		t.Fatal("composer state did not include live workspace status")
	}
	expectStatus(t, b.request(base, "/?run_id="+run, "GET", nil), 404)
	expectStatus(t, b.request(str(second["public_url"]), "/", "GET", nil), 404)
	if dump(b.app.store.stats()) != beforeStats || dump(object(b.app.store.session(run)["snapshot"])) != beforeSnapshot {
		t.Fatal("pause generated or changed experiment evidence")
	}
	resumed := b.api("/workspaces/"+id+"/status", "POST", Doc{"enabled": true, "version": workspace["version"]}, 200)
	if resumed["publication_status"] != "published" || !boolean(resumed["enabled"]) {
		t.Fatal("resume did not restore published release", resumed)
	}
	if resumedRead := b.request(base, "/api/download?file=notes.txt&run_id="+run, "GET", nil); string(resumedRead.body) != string(read.body) {
		t.Fatal("resume changed the original session output")
	}
	for _, raw := range []Doc{{"enabled": "yes"}, {"enabled": false, "version": 0}, {"enabled": false, "version": "1"}} {
		b.api("/workspaces/"+id+"/status", "POST", raw, 400)
	}
	b.api("/workspaces/"+id+"/status", "POST", Doc{"enabled": false, "version": integer(workspace["version"]) + 1}, 409)
	beforeDelete := dump(b.app.store.session(run))
	release := str(deployment["release_id"])
	result := b.api("/workspaces/"+id, "DELETE", nil, 200)
	if !boolean(result["ok"]) || len(array(result["detached_listeners"])) != 2 {
		t.Fatal("workspace deletion did not report both detached listeners", result)
	}
	if entityMaybe(b.app.store.db, "workspaces", id) != nil || entityMaybe(b.app.store.db, "deployments", str(deployment["id"])) != nil {
		t.Fatal("workspace/current deployment survived deletion")
	}
	if get(b.app.store.db, "composer_releases", release)["id"] != release || get(b.app.store.db, "sites_versions", versionKey(site))["id"] != versionKey(site) || dump(b.app.store.session(run)) != beforeDelete {
		t.Fatal("workspace deletion damaged releases, assets or session evidence")
	}
	for _, listener := range []Doc{first, second} {
		stored := get(b.app.store.db, "listeners", str(listener["id"]))
		if boolean(stored["enabled"]) || stored["deployment_id"] != nil || stored["overrides"] != nil {
			t.Fatal("workspace deletion left a bound/enabled port", stored)
		}
	}
	b.client.CloseIdleConnections()
	for _, listener := range []Doc{first, second} {
		if response, err := b.client.Get(str(listener["public_url"]) + "/health"); err == nil {
			response.Body.Close()
			t.Fatal("deleted workspace port still accepts connections")
		}
	}
	if settings(b.app.store.db)["default_deployment"] != nil {
		t.Fatal("default deployment retained a deleted workspace")
	}
	b.restart()
	if dump(b.app.store.session(run)) != beforeDelete || b.app.ListenerCount() != 1 {
		t.Fatal("restart resurrected listener or lost experiment evidence")
	}
	b.api("/workspaces/"+id, "DELETE", nil, 404)
}

func TestWorkspaceDraftDeletionAndPausedPublicationRestart(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	id := str(workspace["id"])
	deleted := b.api("/workspaces/"+id, "DELETE", nil, 200)
	if len(array(deleted["detached_listeners"])) != 0 {
		t.Fatal("draft deletion detached unrelated listeners")
	}
	_, _, workspace = composerFixture(b)
	id = str(workspace["id"])
	deployment := b.api("/workspaces/"+id+"/publish", "POST", Doc{}, 200)
	l := b.listener(deployment)
	b.api("/workspaces/"+id+"/status", "POST", Doc{"enabled": false}, 200)
	b.restart()
	expectStatus(t, b.request(str(l["public_url"]), "/", "GET", nil), 404)
	current := b.api("/workspaces/"+id, "GET", nil, 200)
	if current["publication_status"] != "paused" {
		t.Fatal("restart forgot paused publication")
	}
	b.api("/workspaces/"+id+"/status", "POST", Doc{"enabled": true}, 200)
	expectStatus(t, b.request(str(l["public_url"]), "/", "GET", nil), 200)
}

func TestLegacyPortsCannotAcquireNewBindingsOrBusinessOverrides(t *testing.T) {
	b := newLab(t)
	legacy := b.deployment()
	existing := b.listener(legacy)
	raw := clone(existing)
	delete(raw, "id")
	raw["port"] = b.port()
	raw["public_url"] = "http://127.0.0.1:" + fmtPort(integer(raw["port"]))
	b.api("/listeners", "POST", raw, 400)
	raw["deployment_id"], raw["enabled"] = nil, false
	unbound := b.api("/listeners", "POST", raw, 200)
	unbound["deployment_id"], unbound["enabled"] = legacy["id"], true
	b.api("/listeners", "POST", unbound, 400)
	b.api("/settings", "POST", Doc{"default_deployment": legacy["id"], "public_url": existing["public_url"]}, 200)
	changed := clone(existing)
	changed["overrides"] = Doc{"profile_id": "receipt", "carriers": []any{"api"}, "surfaces": []any{}, "trigger": "page_load"}
	b.api("/listeners", "POST", changed, 400)
	changed = clone(existing)
	changed["root_surface"] = "swagger"
	b.api("/listeners", "POST", changed, 400)
	existing["enabled"] = false
	existing = b.api("/listeners", "POST", existing, 200)
	existing["enabled"] = true
	existing = b.api("/listeners", "POST", existing, 200)
	session, _ := b.visit(existing)
	history := dump(session)
	b.api("/profiles/receipt", "DELETE", nil, 400)
	b.api("/listeners/"+str(existing["id"]), "DELETE", nil, 200)
	b.api("/profiles/receipt", "DELETE", nil, 200)
	if dump(b.app.store.session(str(session["id"]))) != history || entityMaybe(b.app.store.db, "deployments", str(legacy["id"])) == nil {
		t.Fatal("deleting an unused profile damaged preserved legacy data")
	}
}

func TestLegacyCompatibilityPathsRetainProfilesUntilPortRebound(t *testing.T) {
	b := newLab(t)
	direct := b.deployment()
	profile := b.api("/profiles", "POST", Doc{"name": "Other legacy profile", "body": "LEGACY OTHER CANARY", "category": "unexpected_output"}, 200)
	other := clone(direct)
	delete(other, "id")
	other["name"], other["slug"], other["profile_id"] = "Other legacy deployment", "legacy-other", profile["id"]
	other = b.app.store.save("deployments", other)
	listener := b.listener(direct)
	// Reproduce the persisted flag on a primary port upgraded from the old
	// single-port runtime; new management requests cannot enable this flag.
	b.app.store.write(func(q queryer) {
		stored := get(q, "listeners", str(listener["id"]))
		stored["legacy_paths"] = true
		put(q, "listeners", stored)
	})
	base := str(listener["public_url"])
	visit := b.request(base, "/h/"+str(other["slug"]), "GET", nil)
	expectStatus(t, visit, 200)
	run := visit.header.Get("X-Run-ID")
	history := b.app.store.session(run)
	if object(object(history["snapshot"])["profile"])["id"] != profile["id"] {
		t.Fatal("compatibility path did not use the indirectly referenced profile")
	}
	before := dump(history)
	b.api("/profiles/"+str(profile["id"]), "DELETE", nil, 400)
	listener["enabled"] = false
	listener = b.api("/listeners", "POST", listener, 200)
	b.api("/profiles/"+str(profile["id"]), "DELETE", nil, 400)
	listener["enabled"] = true
	listener = b.api("/listeners", "POST", listener, 200)
	expectStatus(t, b.request(base, "/h/"+str(other["slug"]), "GET", nil), 200)

	_, _, workspace := composerFixture(b)
	published := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	listener["deployment_id"] = published["id"]
	listener = b.api("/listeners", "POST", listener, 200)
	if !boolean(listener["legacy_paths"]) {
		t.Fatal("fixture must cover the historical flag remaining after rebinding")
	}
	b.api("/profiles/"+str(profile["id"]), "DELETE", nil, 200)
	if dump(b.app.store.session(run)) != before {
		t.Fatal("removing an unreachable legacy profile changed historical evidence")
	}
}

func TestLegacyTemplateMigrationPreservesOriginalsAndNeverRecreatesDeletedSites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.sqlite3")
	s, err := openStore(path, "http://127.0.0.1:18765")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing(s.db, "templates")) != 0 || len(listing(s.db, "sites")) != 0 {
		t.Fatal("fresh database seeded legacy templates or migrations")
	}
	seedLegacyTemplates(s)
	raw := customTemplateFixture()
	raw["html"] = `<h1>{{title}}</h1><form action="/portal/api/session"><button onclick="alert(1)">Test</button></form><script>window.oldFlow = true</script>{{delivery}}`
	s.save("templates", raw)
	original := dump(listing(s.db, "templates"))
	s.db.Close()
	s, err = openStore(path, "http://127.0.0.1:18765")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.db.Close() }()
	if dump(listing(s.db, "templates")) != original || len(listing(s.db, "sites")) != 5 || len(listing(s.db, "sites_versions")) != 5 {
		t.Fatal("migration lost originals or missed site versions")
	}
	for _, site := range listing(s.db, "sites") {
		content := str(object(array(site["files"])[0])["content"])
		if strings.Contains(content, "<script") || strings.Contains(content, "<form") || strings.Contains(content, "onclick=") || strings.Contains(content, "{{delivery}}") || strings.Contains(content, "href=\"/portal/") {
			t.Fatal("static appearance migration retained legacy behavior", site["name"])
		}
	}
	removed := listing(s.db, "sites")[0]
	s.deleteComposer("sites", str(removed["id"]))
	s.db.Close()
	s, err = openStore(path, "http://127.0.0.1:18765")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing(s.db, "sites")) != 4 || entityMaybe(s.db, "sites", str(removed["id"])) != nil || len(listing(s.db, "legacy_template_migrations")) != 5 || dump(listing(s.db, "templates")) != original {
		t.Fatal("restart repeated migration or changed preserved originals")
	}
}
