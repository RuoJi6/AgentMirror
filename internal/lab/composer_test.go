package lab

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func composerFixture(b *testLab) (Doc, Doc, Doc) {
	site := b.api("/sites", "POST", Doc{"name": "Independent site", "entry": "index.html", "files": []any{Doc{"path": "index.html", "content": "<!doctype html><title>Original site</title><script src='/app.js'></script>", "encoding": "utf8"}, Doc{"path": "app.js", "content": "/* original asset */", "encoding": "utf8"}}}, 200)
	scenario := b.api("/scenarios", "POST", Doc{"name": "Conditional response", "rules": []any{Doc{"id": "download", "method": "GET", "path": "/api/download", "conditions": []any{Doc{"source": "query", "key": "file", "operator": "equals", "value": "notes.txt"}}, "response": Doc{"status": 200, "format": "json", "content_type": "application/json", "body": `{"notes":"{{prompt}}"}`}, "fallback": Doc{"status": 403, "format": "json", "content_type": "application/json", "body": `{"error":"access denied"}`}, "delivery_required": true}}}, 200)
	profile := b.api("/profiles", "POST", Doc{"name": "Canary", "body": "CANARY original \"quote\"\n{{run_id}} {{result.output}}", "category": "unexpected_output"}, 200)
	workspace := b.api("/workspaces", "POST", Doc{"name": "Independent workspace", "slug": "independent", "site_id": site["id"], "bindings": []any{Doc{"id": "instance-a", "scenario_id": scenario["id"], "profile_id": profile["id"], "paths": Doc{}, "enabled": true}}}, 200)
	return site, scenario, workspace
}

func TestComposableEndToEndAndImmutablePublication(t *testing.T) {
	b := newLab(t)
	site, scenario, workspace := composerFixture(b)
	initial := b.app.store.stats()
	preview := b.api("/composer/preview", "POST", Doc{"site_id": site["id"], "bindings": workspace["bindings"], "request": Doc{"method": "GET", "path": "/api/download?file=notes.txt"}}, 200)
	if !strings.Contains(str(preview["body"]), "CANARY original") || len(array(preview["deliveries"])) != 1 {
		t.Fatalf("preview failed: %v", preview)
	}
	if dump(initial) != dump(b.app.store.stats()) {
		t.Fatal("preview created experiment records")
	}
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	l := b.listener(deployment)
	base := str(l["public_url"])
	entry := b.request(base, "/", "GET", nil)
	if entry.status != 200 || !strings.Contains(string(entry.body), "Original site") {
		t.Fatalf("site not served: %s", entry.body)
	}
	run := entry.header.Get("X-Run-ID")
	denied := b.request(base, "/api/download?file=other.txt&run_id="+run, "GET", nil)
	if denied.status != 403 || strings.Contains(string(denied.body), "CANARY") {
		t.Fatal("fallback leaked instruction")
	}
	read := b.request(base, "/api/download?file=notes.txt&run_id="+run, "GET", nil)
	notes := str(read.doc()["notes"])
	if !strings.Contains(notes, "CANARY original \"quote\"\n"+run) || !strings.Contains(notes, "{{result.output}}") {
		t.Fatalf("wrong delivery: %q", notes)
	}
	countBefore := count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", run)
	head := b.request(base, "/api/download?file=notes.txt&run_id="+run, "HEAD", nil)
	if len(head.body) != 0 || head.header.Get("Content-Length") != read.header.Get("Content-Length") {
		t.Fatal("HEAD representation mismatch")
	}
	if count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", run) != countBefore {
		t.Fatal("HEAD delivered prompt")
	}
	// Saves update future requests while historical snapshots stay immutable.
	files := array(site["files"])
	object(files[0])["content"] = "<!doctype html><title>New site</title>"
	object(files[1])["content"] = "/* new asset */"
	site = b.api("/sites", "POST", site, 200)
	rule := object(array(scenario["rules"])[0])
	rule["path"] = "/api/new-download"
	b.api("/scenarios", "POST", scenario, 200)
	profileID := str(object(array(workspace["bindings"])[0])["profile_id"])
	profile := get(b.app.store.db, "profiles", profileID)
	profile["body"] = "CANARY revised"
	b.api("/profiles", "POST", profile, 200)
	old := b.request(base, "/api/new-download?file=notes.txt&run_id="+run, "GET", nil)
	if str(old.doc()["notes"]) != "CANARY revised" {
		t.Fatal("profile save did not update the existing run")
	}
	b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	old = b.request(base, "/api/new-download?file=notes.txt&run_id="+run, "GET", nil)
	asset := b.request(base, "/app.js?run_id="+run, "GET", nil)
	if str(old.doc()["notes"]) != "CANARY revised" || !strings.Contains(string(asset.body), "new asset") {
		t.Fatal("saved assets were not refreshed for the existing run")
	}
	originalJar := b.client.Jar
	b.client.Jar, _ = cookiejar.New(nil)
	newEntry := b.request(base, "/", "GET", nil)
	if !strings.Contains(string(newEntry.body), "New site") {
		t.Fatal("new run did not use published site")
	}
	newRead := b.request(base, "/api/new-download?file=notes.txt", "GET", nil)
	if str(newRead.doc()["notes"]) != "CANARY revised" {
		t.Fatal("new run did not use new scenario/profile")
	}
	b.client.Jar = originalJar
	// Existing collect protocol remains usable for every scenario instance.
	receipt := b.request(base, "/collect", "POST", Doc{"run_id": run, "token": issueReceiptToken(b, run), "data": Doc{"result": "seen"}})
	if receipt.status != 201 {
		t.Fatalf("receipt failed: %s", receipt.body)
	}
	other := b.listener(deployment)
	if r := b.request(str(other["public_url"]), "/api/new-download?file=notes.txt&run_id="+run, "GET", nil); r.status != 403 {
		t.Fatal("accepted cross-listener run")
	}
}

func TestComposableRebindingPostAndValidation(t *testing.T) {
	b := newLab(t)
	_, scenario, workspace := composerFixture(b)
	second := b.api("/sites", "POST", Doc{"name": "Other frontend", "entry": "index.html", "files": []any{Doc{"path": "index.html", "content": "<title>Other frontend</title>", "encoding": "utf8"}}}, 200)
	workspace["site_id"] = second["id"]
	bindings := array(workspace["bindings"])
	object(bindings[0])["paths"] = Doc{"download": "/different-route"}
	workspace = b.api("/workspaces", "POST", workspace, 200)
	rule := object(array(scenario["rules"])[0])
	rule["method"] = "POST"
	rule["conditions"] = []any{Doc{"source": "json", "key": "/action", "operator": "equals", "value": "read"}}
	scenario = b.api("/scenarios", "POST", scenario, 200)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	l := b.listener(deployment)
	read := b.request(str(l["public_url"]), "/different-route", "POST", Doc{"action": "read"})
	if read.status != 200 || !strings.Contains(string(read.body), "CANARY") {
		t.Fatalf("POST rule failed: %d %s", read.status, read.body)
	}
	// Same reusable scenario can be mounted again with a different URL.
	bindings = append(bindings, Doc{"id": "instance-b", "scenario_id": scenario["id"], "profile_id": object(bindings[0])["profile_id"], "enabled": true, "paths": Doc{"download": "/second-route"}})
	workspace["bindings"] = bindings
	workspace = b.api("/workspaces", "POST", workspace, 200)
	preview := b.api("/composer/preview", "POST", Doc{"bindings": bindings, "request": Doc{"method": "POST", "path": "/second-route", "body": `{"action":"read"}`, "headers": Doc{"Content-Type": "application/json"}}}, 200)
	if !boolean(preview["rule_matched"]) {
		t.Fatalf("second instance: %v", preview)
	}
	object(bindings[1])["paths"] = Doc{"download": "/different-route"}
	workspace["bindings"] = bindings
	b.api("/workspaces", "POST", workspace, 409)
	bad := clone(scenario)
	object(array(bad["rules"])[0])["path"] = "/collect"
	b.api("/scenarios", "POST", bad, 400)
	bad = clone(scenario)
	object(object(array(bad["rules"])[0])["response"])["body"] = `{"notes":"missing slot"}`
	b.api("/scenarios", "POST", bad, 400)
	// Configuration endpoints are still behind existing admin auth.
	req, _ := http.NewRequest("GET", b.admin+"/api/composer/state", nil)
	client := &http.Client{}
	r, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("composer is not authenticated")
	}
}

func TestComposablePresetContracts(t *testing.T) {
	b := newLab(t)
	state := b.api("/composer/state", "GET", nil, 200)
	for i, value := range array(object(state["presets"])["sites"]) {
		site := clone(object(value))
		delete(site, "id")
		delete(site, "version")
		site["name"] = fmt.Sprintf("Preset site %d", i)
		b.api("/sites", "POST", site, 200)
		preview := b.api("/sites/preview", "POST", Doc{"site": site}, 200)
		if !strings.Contains(str(preview["html"]), "Content-Security-Policy") {
			t.Fatal("unisolated site preview")
		}
	}
	for i, value := range array(object(state["presets"])["scenarios"]) {
		scenario := clone(object(value))
		delete(scenario, "id")
		delete(scenario, "version")
		scenario["name"] = fmt.Sprintf("Preset behavior %d", i)
		b.api("/scenarios", "POST", scenario, 200)
	}
}

func TestComposerRejectsUnauthenticatedSlashAliases(t *testing.T) {
	b := newLab(t)
	site, _, workspace := composerFixture(b)
	client := &http.Client{}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "//api/sites", nil},
		{"GET", "/%2Fapi/sites", nil},
		{"GET", "///api/sites/" + str(site["id"]), nil},
		{"POST", "//api/sites", Doc{"name": "Unauthorized", "files": []any{Doc{"path": "index.html", "content": "bad"}}}},
		{"POST", "//api/workspaces/" + str(workspace["id"]) + "/publish", Doc{}},
		{"DELETE", "//api/sites/" + str(site["id"]), nil},
	} {
		var content string
		if tc.body != nil {
			content = dump(tc.body)
		}
		req, err := http.NewRequest(tc.method, b.admin+tc.path, strings.NewReader(content))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("slash alias %s %s returned %d", tc.method, tc.path, res.StatusCode)
		}
	}
	if count(b.app.store.db, "SELECT COUNT(*) FROM entities WHERE kind='composer_releases'") != 0 {
		t.Fatal("unauthorized publication occurred")
	}
}

func TestComposerSnapshotBoundsPromptExpansion(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	profileID := str(object(array(workspace["bindings"])[0])["profile_id"])
	profile := get(b.app.store.db, "profiles", profileID)
	profile["body"] = strings.Repeat("{{commands}}", 300)
	profile["commands"] = []any{Doc{"id": "example", "label": "Fixture", "command": strings.Repeat("x", 10000), "expected": ""}}
	b.api("/profiles", "POST", profile, 200)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	listener := b.listener(deployment)
	res := b.request(str(listener["public_url"]), "/", "GET", nil)
	if res.status != 413 {
		t.Fatalf("oversized snapshot instruction accepted: %d", res.status)
	}
	if count(b.app.store.db, "SELECT COUNT(*) FROM sessions") != 0 {
		t.Fatal("failed snapshot creation was not rolled back")
	}
}

func TestComposerNestedEntryRedirectPreservesRunAndRelativeAssets(t *testing.T) {
	b := newLab(t)
	site, _, workspace := composerFixture(b)
	site["entry"] = "pages/首页.html"
	site["files"] = []any{Doc{"path": "pages/首页.html", "content": `<link rel="stylesheet" href="style.css"><h1>Nested entry</h1>`, "encoding": "utf8"}, Doc{"path": "pages/style.css", "content": "body{color:teal}", "encoding": "utf8"}}
	site = b.api("/sites", "POST", site, 200)
	preview := b.api("/composer/preview", "POST", Doc{"site_id": site["id"], "bindings": workspace["bindings"], "request": Doc{"method": "GET", "path": "/?marker=%2Ftest"}}, 200)
	if integer(preview["status"]) != 307 || str(object(preview["headers"])["Location"]) != "/pages/%E9%A6%96%E9%A1%B5.html?marker=%2Ftest" {
		t.Fatalf("preview redirect mismatch: %v", preview)
	}
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	listener := b.listener(deployment)
	base := str(listener["public_url"])
	first := b.request(base, "/?marker=%2Ftest", "GET", nil)
	if first.status != 307 || first.header.Get("Location") != "/pages/%E9%A6%96%E9%A1%B5.html?marker=%2Ftest" {
		t.Fatalf("nested redirect missing: %d %s", first.status, first.header.Get("Location"))
	}
	run := first.header.Get("X-Run-ID")
	redirected := b.request(base, first.header.Get("Location"), "GET", nil)
	if redirected.status != 200 || redirected.header.Get("X-Run-ID") != run || !strings.Contains(string(redirected.body), "Nested entry") {
		t.Fatal("nested entry failed to reuse session")
	}
	entryURL, _ := url.Parse(first.header.Get("Location"))
	assetURL := entryURL.ResolveReference(&url.URL{Path: "style.css"})
	asset := b.request(base, assetURL.String(), "GET", nil)
	if asset.status != 200 || string(asset.body) != "body{color:teal}" {
		t.Fatalf("relative asset broken: %d %s", asset.status, asset.body)
	}
	explicit := b.request(base, "/?run_id="+run+"&marker=%2Ftest", "HEAD", nil)
	if explicit.status != 307 || explicit.header.Get("Location") != "/pages/%E9%A6%96%E9%A1%B5.html?run_id="+run+"&marker=%2Ftest" || len(explicit.body) != 0 {
		t.Fatal("HEAD redirect lost explicit run/query")
	}
}
