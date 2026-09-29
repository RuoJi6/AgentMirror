package lab

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func TestLivePromptsAcrossExistingAndNewSessions(t *testing.T) {
	b := newLab(t)
	site, scenario, workspace := composerFixture(b)
	profileID := str(object(array(workspace["bindings"])[0])["profile_id"])
	v1 := get(b.app.store.db, "profiles", profileID)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	releaseBefore := dump(get(b.app.store.db, "composer_releases", str(deployment["release_id"])))
	l := b.listener(deployment)
	base := str(l["public_url"])
	first := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	run := first.header.Get("X-Run-ID")
	snap := object(b.app.store.session(run)["snapshot"])
	snapshotBefore := dump(snap)
	var firstDelivery string
	for _, raw := range array(b.app.store.session(run)["events"]) {
		e := object(raw)
		if e["kind"] == "delivery" {
			firstDelivery = dump(e)
		}
	}
	if firstDelivery == "" || !strings.Contains(string(first.body), "CANARY original") {
		t.Fatal("missing original delivery")
	}
	// Material saves and profile saves update the same existing session.
	object(array(site["files"])[0])["content"] = "<title>UNPUBLISHED_SITE</title>"
	b.api("/sites", "POST", site, 200)
	object(array(scenario["rules"])[0])["path"] = "/unpublished-path"
	b.api("/scenarios", "POST", scenario, 200)
	v2 := clone(v1)
	v2["body"] = "LIVE_V2 {{run_id}} TOKEN={{token}} {{callback_url}} {{fields}} {{commands}}"
	v2["fields"] = []any{Doc{"name": "result", "type": "string", "required": false, "description": "FIELD_V2"}}
	v2["commands"] = []any{Doc{"id": "marker", "label": "Fixture", "command": "SYNTHETIC_COMMAND_V2", "expected": ""}}
	v2 = b.api("/profiles", "POST", v2, 200)
	for _, path := range []string{"/unpublished-path?file=notes.txt", "/unpublished-path?file=notes.txt&run_id=" + run} {
		r := b.request(base, path, "GET", nil)
		if r.status != 200 || r.header.Get("X-Run-ID") != run || r.header.Get("Cache-Control") != "no-store" {
			t.Fatal("existing run was not reused", r)
		}
		for _, marker := range []string{"LIVE_V2", run, responseToken(t, str(r.doc()["notes"])), str(snap["callback_url"]), "FIELD_V2", "SYNTHETIC_COMMAND_V2"} {
			if !strings.Contains(str(r.doc()["notes"]), marker) {
				t.Fatal("latest profile or original context missing", marker)
			}
		}
	}
	req, _ := http.NewRequest("GET", base+"/unpublished-path?file=notes.txt", nil)
	req.Header.Set("X-Run-ID", run)
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(content), "LIVE_V2") {
		t.Fatal("header session did not hot update")
	}
	if r := b.request(base, "/unpublished-path?file=notes.txt", "GET", nil); r.status != 200 {
		t.Fatal("saved rules were not refreshed")
	}
	if r := b.request(base, "/", "GET", nil); !strings.Contains(string(r.body), "UNPUBLISHED_SITE") {
		t.Fatal("saved site was not refreshed")
	}
	if r := b.request(base, "/unpublished-path?file=other.txt", "GET", nil); r.status != 403 || strings.Contains(string(r.body), "LIVE_V2") {
		t.Fatal("ordinary response leaked prompt")
	}
	beforeHead := count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", run)
	b.request(base, "/unpublished-path?file=notes.txt", "HEAD", nil)
	if count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", run) != beforeHead {
		t.Fatal("HEAD was recorded as delivery")
	}
	history := b.app.store.session(run)
	if dump(object(history["snapshot"])) != snapshotBefore {
		t.Fatal("historical snapshot mutated")
	}
	if integer(object(array(b.app.store.sessionView(run, 1)["current_profiles"])[0])["version"]) != 2 {
		t.Fatal("current version metadata stale")
	}
	foundOriginal, foundLatest := false, false
	for _, raw := range array(history["events"]) {
		e := object(raw)
		foundOriginal = foundOriginal || dump(e) == firstDelivery
		if e["kind"] == "delivery" {
			detail := object(e["detail"])
			for _, d := range array(detail["deliveries"]) {
				if integer(object(d)["profile_version"]) == 2 && strings.Contains(str(detail["body"]), "LIVE_V2") {
					foundLatest = true
				}
			}
		}
	}
	if !foundOriginal || !foundLatest {
		t.Fatal("delivery history lost actual versions")
	}
	other := b.listener(deployment)
	oldJar := b.client.Jar
	b.client.Jar, _ = cookiejar.New(nil)
	newRead := b.request(base, "/unpublished-path?file=notes.txt", "GET", nil)
	if !strings.Contains(string(newRead.body), "LIVE_V2") || newRead.header.Get("X-Run-ID") == run {
		t.Fatal("new session used published old profile")
	}
	if r := b.request(str(other["public_url"]), "/unpublished-path?file=notes.txt", "GET", nil); !strings.Contains(string(r.body), "LIVE_V2") {
		t.Fatal("other port stale")
	}
	if r := b.request(str(other["public_url"]), "/unpublished-path?file=notes.txt&run_id="+run, "GET", nil); r.status != 403 {
		t.Fatal("hot update broke isolation")
	}
	b.client.Jar = oldJar
	b.api("/profiles", "POST", v1, 409)
	restored := clone(v1)
	restored["version"] = v2["version"]
	b.api("/profiles", "POST", restored, 200)
	if r := b.request(base, "/unpublished-path?file=notes.txt&run_id="+run, "GET", nil); string(r.body) != string(first.body) {
		t.Fatal("restored profile did not hot update")
	}
	if dump(get(b.app.store.db, "composer_releases", str(deployment["release_id"]))) != releaseBefore {
		t.Fatal("release snapshot mutated")
	}
	b.restart()
	if r := b.request(base, "/unpublished-path?file=notes.txt&run_id="+run, "GET", nil); string(r.body) != string(first.body) {
		t.Fatal("hot update lost after restart")
	}
	if r := b.request(base, "/collect", "POST", Doc{"run_id": run, "token": snap["token"], "data": "AFTER_HOT_UPDATE"}); r.status != 403 {
		t.Fatal("original receipt token stayed valid after later requests")
	}
}

func TestLivePromptResolutionKeepsBindingsAndMissingHistory(t *testing.T) {
	b := newLab(t)
	first := b.api("/profiles", "POST", Doc{"name": "First", "body": "V1", "category": "unexpected_output"}, 200)
	second := b.api("/profiles", "POST", Doc{"name": "Second", "body": "SECOND", "category": "unexpected_output"}, 200)
	latest := clone(first)
	latest["body"] = "V2"
	b.api("/profiles", "POST", latest, 200)
	snap := Doc{"deployment": Doc{"mode": "composable"}, "profile": first, "rules": []any{Doc{"profile": first}, Doc{"profile": second}, Doc{"profile": first}, Doc{"profile": nil}}}
	b.app.store.write(func(q queryer) { refreshSessionPrompts(q, Doc{"id": "run", "snapshot": snap}) })
	rules := array(snap["rules"])
	if object(object(rules[0])["profile"])["body"] != "V2" || object(object(rules[2])["profile"])["body"] != "V2" || object(object(rules[1])["profile"])["body"] != "SECOND" || object(rules[3])["profile"] != nil {
		t.Fatal("changed binding identity")
	}
	b.app.store.delete("profiles", str(first["id"]))
	b.app.store.write(func(q queryer) {
		p := liveProfiles{q: q}
		if p.resolve(first)["body"] != "V1" {
			t.Fatal("missing historical profile lost snapshot fallback")
		}
	})
}
