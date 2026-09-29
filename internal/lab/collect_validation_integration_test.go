package lab

import (
	"net/http"
	"testing"
)

func diagnosticValidationFixture() Doc {
	return Doc{"enabled": true, "fields": []any{
		Doc{"name": "host", "type": "string", "required": true, "max_length": 128},
		Doc{"name": "user", "type": "string", "required": true, "max_length": 128},
		Doc{"name": "ip", "type": "ip", "required": true, "max_length": 64},
		Doc{"name": "ts", "type": "datetime", "required": true, "max_length": 64},
	}}
}

func TestWorkspaceCollectValidationLifecycle(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	id := str(workspace["id"])
	dep := b.api("/workspaces/"+id+"/publish", "POST", Doc{}, 200)
	l := b.listener(dep)
	base := str(l["public_url"])
	visit := b.request(base, "/", "GET", nil)
	run := visit.header.Get("X-Run-ID")
	snap := object(b.app.store.session(run)["snapshot"])
	body := Doc{"run_id": run, "token": snap["token"], "data": "LEGACY_FREEFORM"}
	expectStatus(t, b.request(base, "/collect", "POST", body), 201)
	endpoint := "/workspaces/" + id + "/collect-validation"
	if boolean(b.api(endpoint, "GET", nil, 200)["enabled"]) {
		t.Fatal("existing workspace must remain freeform by default")
	}
	config := diagnosticValidationFixture()
	config["version"] = workspace["version"]
	workspace = b.api(endpoint, "POST", config, 200)
	if workspace["version"] != workspace["published_version"] || !boolean(collectValidationConfig(get(b.app.store.db, "deployments", str(dep["id"])))["enabled"]) {
		t.Fatal("policy was not published with workspace save")
	}
	b.api(endpoint, "POST", config, 409)
	before := count(b.app.store.db, "SELECT COUNT(*) FROM reports")
	for _, value := range []any{
		"RANDOM_PROBE", Doc{}, Doc{"host": "x", "user": "y", "ip": "random-text", "ts": "random-text"},
		Doc{"host": "x", "user": "y", "ip": "192.0.2.1", "ts": "not-a-date"},
	} {
		bad := merge(clone(body), Doc{"data": value})
		r := b.request(base, "/collect", "POST", bad)
		if r.status != 422 || r.doc()["ok"] != false || object(r.doc()["validation"])["execution"] != "unverified" || r.header.Get("X-Receipt-Schema") != "failed" {
			t.Fatal("invalid data accepted", r.status, string(r.body))
		}
	}
	if count(b.app.store.db, "SELECT COUNT(*) FROM reports") != before || count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE kind='receipt_rejected'") != 4 {
		t.Fatal("invalid receipt stored or missing rejection evidence")
	}
	validData := Doc{"host": " synthetic-host ", "user": "synthetic-user", "ip": "192.0.2.10", "ts": "2026-09-19T12:00:00Z"}
	body["data"] = validData
	r := b.request(base, "/collect", "POST", body)
	if r.status != 201 || object(r.doc()["validation"])["schema"] != "passed" || object(r.doc()["validation"])["execution"] != "unverified" {
		t.Fatal("schema validity confused with execution evidence", r.status, string(r.body))
	}
	stored := rows(b.app.store.db, "SELECT body FROM reports ORDER BY id DESC LIMIT 1")[0]
	if object(decode(str(stored["body"]))["data"])["host"] != validData["host"] {
		t.Fatal("submitted values were rewritten")
	}
	// Authentication remains first, including when the submitted data is invalid.
	expectStatus(t, b.request(base, "/collect", "POST", merge(clone(body), Doc{"token": "invented", "data": nil})), 403)
	other := b.listener(dep)
	expectStatus(t, b.request(str(other["public_url"]), "/collect", "POST", body), 403)
	// Global presentation customization cannot turn a validation rejection into success.
	custom := defaultCollectResponse()
	custom["enabled"], custom["status"], custom["body"] = true, 202, `{"stored":true}`
	custom["headers"] = Doc{"x-execution-evidence": "verified", "x-receipt-schema": "fake"}
	b.api("/collect-response", "POST", custom, 200)
	expectStatus(t, b.request(base, "/collect", "POST", merge(clone(body), Doc{"data": "bad"})), 422)
	r = b.request(base, "/collect", "POST", body)
	if r.status != 202 || r.header.Get("X-Execution-Evidence") != "unverified" || r.header.Get("X-Receipt-Schema") != "passed" {
		t.Fatal("custom response lost authoritative evidence metadata")
	}
	b.restart()
	if !boolean(b.api(endpoint, "GET", nil, 200)["enabled"]) {
		t.Fatal("policy did not survive restart")
	}
	config["version"], config["enabled"] = workspace["version"], false
	b.api(endpoint, "POST", config, 200)
	custom["headers"] = Doc{}
	b.api("/collect-response", "POST", custom, 200)
	r = b.request(base, "/collect", "POST", merge(clone(body), Doc{"data": "FREEFORM_AGAIN"}))
	if r.status != 202 || r.header.Get("X-Receipt-Schema") != "" {
		t.Fatal("disabling policy did not restore freeform receipt handling")
	}
	client := &http.Client{}
	res, err := client.Get(b.admin + "/api" + endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("management policy was exposed without authentication")
	}
}

func TestWorkspaceCollectValidationIsolationAndSaveCompatibility(t *testing.T) {
	b := newLab(t)
	_, _, w := composerFixture(b)
	w["collect_validation"] = diagnosticValidationFixture()
	w = b.api("/workspaces", "POST", w, 200)
	// Old clients omitting the new field retain the configured policy.
	delete(w, "collect_validation")
	w = b.api("/workspaces", "POST", w, 200)
	if !boolean(collectValidationConfig(w)["enabled"]) {
		t.Fatal("legacy save discarded policy")
	}
	other := clone(w)
	delete(other, "id")
	delete(other, "collect_validation")
	other["name"], other["slug"] = "Unrelated workspace", "unrelated-validation"
	other = b.api("/workspaces", "POST", other, 200)
	if boolean(collectValidationConfig(other)["enabled"]) {
		t.Fatal("policy leaked to unrelated workspace")
	}
}
