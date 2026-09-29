package lab

import (
	"net/http/cookiejar"
	"strings"
	"testing"
)

func TestRuleProfilesPreviewPublicationAndHotUpdate(t *testing.T) {
	b := newLab(t)
	_, scenario, workspace := composerFixture(b)
	binding := object(array(workspace["bindings"])[0])
	profileA := get(b.app.store.db, "profiles", str(binding["profile_id"]))
	profileA["body"] = "PROFILE_A {{run_id}}"
	profileA = b.api("/profiles", "POST", profileA, 200)
	profileB := b.api("/profiles", "POST", Doc{"name": "Interface B", "body": "PROFILE_B {{run_id}}", "category": "unexpected_output"}, 200)
	profileC := b.api("/profiles", "POST", Doc{"name": "Interface C", "body": "PROFILE_C {{run_id}}", "category": "unexpected_output"}, 200)
	config := clone(object(array(scenario["rules"])[0]))
	config["id"], config["name"] = "config", "Config branch"
	config["conditions"] = []any{Doc{"source": "query", "key": "file", "operator": "equals", "value": "config.txt"}}
	post := clone(config)
	post["id"], post["name"], post["method"], post["path"], post["conditions"] = "submit", "Submission", "POST", "/api/submit", []any{}
	plain := clone(config)
	plain["id"], plain["path"], plain["conditions"], plain["delivery_required"] = "plain", "/api/home", []any{}, false
	plain["response"] = Doc{"status": 200, "format": "text", "content_type": "text/plain", "body": "ORDINARY_FILE"}
	scenario["rules"] = append(array(scenario["rules"]), config, post, plain)
	scenario = b.api("/scenarios", "POST", scenario, 200)
	binding["rule_profiles"] = Doc{"config": profileB["id"], "submit": profileC["id"]}
	workspace["bindings"] = append(array(workspace["bindings"]), Doc{
		"id": "second-instance", "scenario_id": scenario["id"], "profile_id": profileA["id"], "enabled": true,
		"paths": Doc{"download": "/other/download", "config": "/other/download", "submit": "/other/submit", "plain": "/other/home"},
	})
	workspace = b.api("/workspaces", "POST", workspace, 200)
	before := dump(b.app.store.stats())
	cases := []struct{ method, path, marker, profileID, ruleID string }{
		{"GET", "/api/download?file=notes.txt", "PROFILE_A", str(profileA["id"]), "download"},
		{"GET", "/api/download?file=config.txt", "PROFILE_B", str(profileB["id"]), "config"},
		{"POST", "/api/submit", "PROFILE_C", str(profileC["id"]), "submit"},
		{"GET", "/other/download?file=config.txt", "PROFILE_A", str(profileA["id"]), "config"},
	}
	for _, c := range cases {
		preview := b.api("/composer/preview", "POST", Doc{"bindings": workspace["bindings"], "request": Doc{"method": c.method, "path": c.path}}, 200)
		if !strings.Contains(str(preview["body"]), c.marker+" PREVIEW_RUN") || object(array(preview["deliveries"])[0])["profile_id"] != c.profileID {
			t.Fatal("preview did not use rule-specific profile", c.path)
		}
	}
	if dump(b.app.store.stats()) != before {
		t.Fatal("preview wrote experimental records")
	}
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	l := b.listener(deployment)
	base := str(l["public_url"])
	run := ""
	for _, c := range cases {
		r := b.request(base, c.path, c.method, nil)
		if run == "" {
			run = r.header.Get("X-Run-ID")
		}
		if r.status != 200 || !strings.Contains(string(r.body), c.marker+" "+run) {
			t.Fatal("published response mixed up rule profiles", c.path)
		}
		events := array(b.app.store.session(run)["events"])
		last := object(events[len(events)-1])
		delivery := object(array(object(last["detail"])["deliveries"])[0])
		if delivery["profile_id"] != c.profileID || delivery["rule_id"] != c.ruleID {
			t.Fatal("delivery recorded incorrect rule/profile identity", c.path)
		}
	}
	original := b.app.store.session(run)
	bindings := array(workspace["bindings"])
	binding = object(bindings[0])
	binding["rule_profiles"] = Doc{"config": profileC["id"], "submit": profileB["id"]}
	binding["paths"] = Doc{"config": "/unpublished-route"}
	workspace = b.api("/workspaces", "POST", workspace, 200)
	checkResponse := func(path, method, marker string) {
		t.Helper()
		r := b.request(base, path, method, nil)
		if r.status != 200 || !strings.Contains(string(r.body), marker+" "+run) || r.header.Get("X-Run-ID") != run {
			t.Fatal("save did not update only the assigned rule", path)
		}
	}
	checkResponse("/unpublished-route?file=config.txt", "GET", "PROFILE_C")
	checkResponse("/api/download?file=notes.txt", "GET", "PROFILE_A")
	checkResponse("/api/submit", "POST", "PROFILE_B")
	checkResponse("/other/download?file=config.txt", "GET", "PROFILE_A")
	if r := b.request(base, "/unpublished-route?file=config.txt", "GET", nil); r.status != 200 {
		t.Fatal("saving prompt bindings did not update the path")
	}
	// Returning a rule to inheritance follows the current default, while
	// explicit overrides remain independent when that default changes.
	binding = object(array(workspace["bindings"])[0])
	binding["profile_id"] = profileC["id"]
	binding["rule_profiles"] = Doc{"submit": profileB["id"]}
	workspace = b.api("/workspaces", "POST", workspace, 200)
	checkResponse("/unpublished-route?file=config.txt", "GET", "PROFILE_C")
	checkResponse("/api/download?file=notes.txt", "GET", "PROFILE_C")
	checkResponse("/api/submit", "POST", "PROFILE_B")
	profileB["body"] = "PROFILE_B_V2 {{run_id}}"
	b.api("/profiles", "POST", profileB, 200)
	checkResponse("/api/submit", "POST", "PROFILE_B_V2")
	countBefore := count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", run)
	if r := b.request(base, "/api/home", "GET", nil); string(r.body) != "ORDINARY_FILE" {
		t.Fatal("ordinary response changed")
	}
	if r := b.request(base, "/api/download?file=unknown", "GET", nil); r.status != 403 || strings.Contains(string(r.body), "PROFILE_") {
		t.Fatal("fallback leaked prompt")
	}
	b.request(base, "/api/download?file=config.txt", "HEAD", nil)
	if count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", run) != countBefore {
		t.Fatal("ordinary/fallback/HEAD response counted as delivery")
	}
	if get(b.app.store.db, "deployments", str(deployment["id"]))["release_id"] == deployment["release_id"] {
		t.Fatal("rule binding save did not advance release")
	}
	after := b.app.store.session(run)
	if dump(original["snapshot"]) != dump(after["snapshot"]) {
		t.Fatal("hot rule assignment rewrote session snapshot")
	}
	for i, event := range array(original["events"]) {
		if dump(event) != dump(array(after["events"])[i]) {
			t.Fatal("hot rule assignment rewrote past delivery")
		}
	}
	jar := b.client.Jar
	b.client.Jar, _ = cookiejar.New(nil)
	if r := b.request(base, "/api/submit", "POST", nil); !strings.Contains(string(r.body), "PROFILE_B_V2") || r.header.Get("X-Run-ID") == run {
		t.Fatal("new session did not follow saved rule profile")
	}
	b.client.Jar = jar
	b.restart()
	checkResponse("/api/submit", "POST", "PROFILE_B_V2")
}

func TestRuleProfileValidationAndReferences(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	id := object(array(workspace["bindings"])[0])["profile_id"]
	for _, overrides := range []any{"wrong type", []any{}, Doc{"unknown": id}, Doc{"download": 42}} {
		bad := clone(workspace)
		object(array(bad["bindings"])[0])["rule_profiles"] = overrides
		b.api("/workspaces", "POST", bad, 400)
	}
	bad := clone(workspace)
	object(array(bad["bindings"])[0])["rule_profiles"] = Doc{"download": "missing-profile"}
	b.api("/workspaces", "POST", bad, 404)
	binding := object(array(workspace["bindings"])[0])
	binding["profile_id"], binding["rule_profiles"] = "", Doc{"download": id}
	workspace = b.api("/workspaces", "POST", workspace, 200)
	b.api("/profiles/"+str(id), "DELETE", nil, 400)
	preview := b.api("/composer/preview", "POST", Doc{"bindings": workspace["bindings"], "request": Doc{"method": "GET", "path": "/api/download?file=notes.txt"}}, 200)
	if object(array(preview["deliveries"])[0])["profile_id"] != id {
		t.Fatal("rule override incorrectly required an instance default")
	}
	bad = clone(workspace)
	object(array(bad["bindings"])[0])["rule_profiles"] = Doc{}
	b.api("/workspaces", "POST", bad, 400)
	// Legacy bindings without rule_profiles still inherit the default.
	legacy := clone(workspace)
	legacyBinding := object(array(legacy["bindings"])[0])
	legacyBinding["profile_id"] = id
	delete(legacyBinding, "rule_profiles")
	b.api("/workspaces", "POST", legacy, 200)
}
