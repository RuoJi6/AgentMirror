package lab

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCollectCustomResponseAndPersistence(t *testing.T) {
	b := newLab(t)
	// Configuring a response does not require a listener and cannot alter port settings.
	before := settings(b.app.store.db)
	cfg := b.api("/collect-response", "GET", nil, 200)
	if boolean(cfg["enabled"]) {
		t.Fatal("custom response enabled by default")
	}
	cfg["enabled"], cfg["status"] = true, 202
	cfg["body"] = `{"message":"收到数据","receipt":"{{receipt_id}}","session":"{{run_id}}","nested":["ID {{receipt_id}}"],"literal":"{{token}}"}`
	cfg["headers"] = Doc{"X-Receipt": "accepted"}
	b.api("/collect-response", "POST", cfg, 200)
	preview := b.api("/collect-response/preview", "POST", cfg, 200)
	if integer(preview["status"]) != 202 || decode(str(preview["body"]))["receipt"] != "123" || count(b.app.store.db, "SELECT COUNT(*) FROM reports") != 0 {
		t.Fatal("preview must render only", preview)
	}
	if settings(b.app.store.db)["public_url"] != before["public_url"] {
		t.Fatal("changed listener settings")
	}
	deployment := b.deployment()
	l := b.listener(deployment)
	session, _ := b.visit(l)
	run := str(session["id"])
	snap := object(session["snapshot"])
	body := Doc{"run_id": run, "token": snap["token"], "data": Doc{"message": "CLIENT_RESULT"}}
	base := str(l["public_url"])
	r := b.request(base, "/collect", "POST", body)
	if r.status != 202 || r.doc()["session"] != run || r.doc()["message"] != "收到数据" || r.header.Get("X-Receipt") != "accepted" || r.doc()["literal"] != "{{token}}" {
		t.Fatalf("custom receipt response: %d %s", r.status, r.body)
	}
	stored := rows(b.app.store.db, "SELECT id,body FROM reports WHERE session_id=?", run)
	if len(stored) != 1 || r.doc()["receipt"] != fmt.Sprint(stored[0]["id"]) || !strings.Contains(str(stored[0]["body"]), "CLIENT_RESULT") || strings.Contains(string(r.body), str(snap["token"])) {
		t.Fatal("receipt storage or ID substitution incorrect", stored, string(r.body))
	}
	for _, change := range []Doc{{"token": "wrong"}, {"run_id": "not-a-session"}} {
		bad := merge(clone(body), change)
		denied := b.request(base, "/collect", "POST", bad)
		if denied.status != 403 || strings.Contains(string(denied.body), "收到数据") {
			t.Fatal("custom response bypassed authentication", denied)
		}
	}
	missing := b.request(base, "/collect", "POST", Doc{"run_id": run, "token": snap["token"]})
	if missing.status != 400 || count(b.app.store.db, "SELECT COUNT(*) FROM reports") != 1 {
		t.Fatal("invalid receipt was stored")
	}
	other := b.listener(deployment)
	if denied := b.request(str(other["public_url"]), "/collect", "POST", body); denied.status != 403 {
		t.Fatal("accepted cross-port receipt")
	}
	// Success responses can be non-JSON without affecting raw receipt storage.
	cfg["format"], cfg["content_type"], cfg["status"], cfg["body"] = "text", "text/plain; charset=utf-8", 200, "已接收 {{receipt_id}} / {{run_id}}"
	b.api("/collect-response", "POST", cfg, 200)
	r = b.request(base, "/collect", "POST", body)
	if r.status != 200 || !strings.Contains(string(r.body), " / "+run) || !strings.HasPrefix(r.header.Get("Content-Type"), "text/plain") {
		t.Fatal("text response failed", string(r.body))
	}
	b.restart()
	if b.api("/collect-response", "GET", nil, 200)["body"] != cfg["body"] {
		t.Fatal("configuration did not survive restart")
	}
	cfg["enabled"] = false
	b.api("/collect-response", "POST", cfg, 200)
	r = b.request(base, "/collect", "POST", body)
	if r.status != 201 || r.doc()["ok"] != true || integer(r.doc()["receipt_id"]) == 0 || r.header.Get("X-Receipt") != "" {
		t.Fatal("default response not restored", r)
	}
}

func TestCollectResponseValidationAndTypedRendering(t *testing.T) {
	b := newLab(t)
	for _, edit := range []Doc{
		{"enabled": "yes"}, {"status": 199}, {"status": 600}, {"status": 204},
		{"format": "json", "body": `{"receipt":{{receipt_id}}}`},
		{"body": strings.Repeat("x", scenarioBodyLimit+1)},
		{"headers": Doc{"X-Test": "x\r\ninjected:y"}},
		{"headers": Doc{"Set-Cookie": "x=y"}},
		{"headers": Doc{"Content-Length": "1"}},
		{"content_type": "text/html"},
	} {
		b.api("/collect-response", "POST", merge(defaultCollectResponse(), edit), 400)
	}
	cfg := defaultCollectResponse()
	cfg["enabled"] = true
	cfg["body"] = `{"message":"{{run_id}}","receipt":"{{receipt_id}}"}`
	result := renderCollectResponse(validateCollectResponse(cfg), "quote\"\n<script>", Doc{"ok": true, "receipt_id": 7})
	if decode(str(result["body"]))["message"] != "quote\"\n<script>" {
		t.Fatal("JSON substitution was not escaped")
	}
	cfg["format"], cfg["content_type"], cfg["body"] = "html", "text/html; charset=utf-8", "<p>{{run_id}} / {{receipt_id}}</p>"
	result = renderCollectResponse(validateCollectResponse(cfg), "<img>", Doc{"receipt_id": 7})
	if result["body"] != "<p>&lt;img&gt; / 7</p>" {
		t.Fatal("HTML substitution was not escaped", result)
	}
	cfg["status"], cfg["body"] = 204, ""
	if result = renderCollectResponse(validateCollectResponse(cfg), "run", Doc{"receipt_id": 7}); result["body"] != "" {
		t.Fatal("no-content response not empty")
	}
	// Preview/save are management operations, protected before handler execution.
	client := &http.Client{}
	r, err := client.Get(b.admin + "/api/collect-response")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("unauthenticated response config access")
	}
}

func TestExpiredTokenCustomResponseNeverStoresRejectedData(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	workspace["callback_path"] = "/receipts"
	workspace = b.api("/workspaces", "POST", workspace, 200)
	profile := get(b.app.store.db, "profiles", str(object(array(workspace["bindings"])[0])["profile_id"]))
	profile["body"] = "TOKEN={{token}}"
	b.api("/profiles", "POST", profile, 200)
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	l := b.listener(dep)
	base := str(l["public_url"])
	first := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	run := first.header.Get("X-Run-ID")
	old := responseToken(t, str(first.doc()["notes"]))
	second := b.request(base, "/api/download?file=notes.txt&run_id="+run, "GET", nil)
	current := responseToken(t, str(second.doc()["notes"]))
	body := Doc{"run_id": run, "token": old, "data": "MUST_NOT_BE_STORED"}
	expectStatus(t, b.request(base, "/receipts", "POST", body), 403)
	endpoint := "/workspaces/" + str(workspace["id"]) + "/collect-rejected-response"
	cfg := b.api(endpoint, "GET", nil, 200)
	if boolean(cfg["enabled"]) || integer(cfg["status"]) != 403 {
		t.Fatal("expired token response should default to 403")
	}
	saveResponse := func(value Doc) {
		t.Helper()
		value["version"] = get(b.app.store.db, "workspaces", str(workspace["id"]))["version"]
		saved := b.api(endpoint, "POST", value, 200)
		if saved["version"] != saved["published_version"] {
			t.Fatal("rejection response did not sync published workspace")
		}
	}
	cfg["enabled"], cfg["status"] = true, 401
	cfg["body"] = `{"error":"TOKEN_EXPIRED","run":"{{run_id}}","literal":"{{receipt_id}}"}`
	cfg["headers"] = Doc{"X-Rejection": "expired"}
	saveResponse(cfg)
	preview := b.api(endpoint+"/preview", "POST", cfg, 200)
	if integer(preview["status"]) != 401 || decode(str(preview["body"]))["run"] != "PREVIEW_RUN" {
		t.Fatal("rejection preview did not render")
	}
	for _, token := range []string{old, "unknown-token"} {
		r := b.request(base, "/receipts", "POST", merge(clone(body), Doc{"token": token}))
		if r.status != 401 || r.doc()["error"] != "TOKEN_EXPIRED" || r.doc()["run"] != run || r.doc()["literal"] != "{{receipt_id}}" || r.header.Get("X-Rejection") != "expired" {
			t.Fatal("custom expired/invalid token response failed")
		}
	}
	// Another workspace can use a different response on the same callback path.
	secondWorkspace := clone(workspace)
	delete(secondWorkspace, "id")
	secondWorkspace["name"], secondWorkspace["slug"] = "Other response", "other-response"
	secondConfig := defaultCollectRejectedResponse()
	secondConfig["enabled"], secondConfig["status"], secondConfig["body"] = true, 410, `{"error":"OTHER_WORKSPACE"}`
	secondWorkspace["collect_rejected_response"] = secondConfig
	secondWorkspace = b.api("/workspaces", "POST", secondWorkspace, 200)
	secondDep := b.api("/workspaces/"+str(secondWorkspace["id"])+"/publish", "POST", Doc{}, 200)
	secondListener := b.listener(secondDep)
	secondBase := str(secondListener["public_url"])
	otherVisit := b.request(secondBase, "/api/download?file=notes.txt", "GET", nil)
	otherBody := Doc{"run_id": otherVisit.header.Get("X-Run-ID"), "token": "invalid", "data": "DO_NOT_STORE"}
	if r := b.request(secondBase, "/receipts", "POST", otherBody); r.status != 410 || r.doc()["error"] != "OTHER_WORKSPACE" {
		t.Fatal("workspaces did not keep independent rejection responses")
	}
	expectStatus(t, b.request(base, "/receipts", "POST", body), 401)
	other := b.listener(dep)
	expectStatus(t, b.request(str(other["public_url"]), "/receipts", "POST", body), 403)
	// Even a success-looking custom HTTP status cannot turn rejection into acceptance.
	cfg["status"], cfg["format"], cfg["content_type"], cfg["body"] = 200, "text", "text/plain; charset=utf-8", "REJECTED {{run_id}}"
	saveResponse(cfg)
	r := b.request(base, "/receipts", "POST", body)
	if r.status != 200 || string(r.body) != "REJECTED "+run || count(b.app.store.db, "SELECT COUNT(*) FROM reports") != 0 || count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE kind='receipt'") != 0 {
		t.Fatal("rejected callback was stored or rejection template failed")
	}
	b.restart()
	if b.api(endpoint, "GET", nil, 200)["body"] != cfg["body"] {
		t.Fatal("rejection configuration lost after restart")
	}
	r = b.request(base, "/receipts", "POST", merge(clone(body), Doc{"token": current, "data": "VALID_SYNTHETIC"}))
	if r.status != 201 || r.doc()["ok"] != true || r.header.Get("X-Rejection") != "" || count(b.app.store.db, "SELECT COUNT(*) FROM reports") != 1 {
		t.Fatal("rejection settings changed valid callback behavior")
	}
	cfg["enabled"] = false
	saveResponse(cfg)
	expectStatus(t, b.request(base, "/receipts", "POST", body), 403)
	expectStatus(t, b.request(secondBase, "/receipts", "POST", otherBody), 410)
	b.api(endpoint, "POST", cfg, 409) // the saved version was consumed
	cfg["version"] = get(b.app.store.db, "workspaces", str(workspace["id"]))["version"]
	b.api(endpoint, "POST", merge(clone(cfg), Doc{"status": 600}), 400)
	b.api(endpoint+"/preview", "POST", merge(clone(cfg), Doc{"headers": Doc{"X-Test": "a\r\nb"}}), 400)
}
