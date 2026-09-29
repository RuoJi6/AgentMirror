package lab

import (
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

var receiptTokenPattern = regexp.MustCompile("TOKEN=([A-Za-z0-9_-]+)")

func responseToken(t *testing.T, text string) string {
	t.Helper()
	matches := receiptTokenPattern.FindStringSubmatch(text)
	if len(matches) != 2 || len(matches[1]) != 32 {
		t.Fatal("response did not render a 24-byte token")
	}
	return matches[1]
}

// Authentication fixtures for unrelated lifecycle tests. Public rendering and
// strict invalidation are exercised in the end-to-end test below.
func issueReceiptToken(b *testLab, run string) string {
	b.t.Helper()
	session := b.app.store.session(run)
	b.app.store.write(func(q queryer) { rotateSessionToken(q, session) })
	return str(object(session["snapshot"])["token"])
}

func TestPublicRequestsRotateTokensAndCustomCallbacksFollowListener(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	workspace["callback_path"] = "/receipts/agent"
	workspace = b.api("/workspaces", "POST", workspace, 200)
	profileID := str(object(array(workspace["bindings"])[0])["profile_id"])
	profile := get(b.app.store.db, "profiles", profileID)
	profile["body"] = "TOKEN={{token}} URL={{callback_url}} RUN={{run_id}}"
	b.api("/profiles", "POST", profile, 200)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	l := b.listener(deployment)
	other := b.listener(deployment)
	base := str(l["public_url"])
	first := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	run := first.header.Get("X-Run-ID")
	firstToken := responseToken(t, str(first.doc()["notes"]))
	if !strings.Contains(str(first.doc()["notes"]), "URL="+base+"/receipts/agent") {
		t.Fatal("callback does not use the current listener origin/path")
	}
	history := b.app.store.session(run)
	snapshot, oldEvents := dump(history["snapshot"]), dump(history["events"])
	collect := func(origin, path, token string, want int) {
		t.Helper()
		expectStatus(t, b.request(origin, path, "POST", Doc{"run_id": run, "token": token, "data": "SYNTHETIC_RESULT"}), want)
	}
	// A database upgraded from pre-rotation versions has no mutable token row.
	b.app.store.write(func(q queryer) { exec(q, "DELETE FROM session_tokens WHERE session_id=?", run) })
	collect(base, "/receipts/agent", firstToken, 201)
	second := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	secondToken := responseToken(t, str(second.doc()["notes"]))
	if firstToken == secondToken || second.header.Get("X-Run-ID") != run {
		t.Fatal("token did not rotate within the same session")
	}
	collect(base, "/receipts/agent", firstToken, 403)
	collect(base, "/receipts/agent", secondToken, 201)
	collect(base, "/receipts/agent", "invalid", 403)
	collect(str(other["public_url"]), "/receipts/agent", secondToken, 403)
	otherResponse := b.request(str(other["public_url"]), "/api/download?file=notes.txt", "GET", nil)
	if !strings.Contains(str(otherResponse.doc()["notes"]), "URL="+str(other["public_url"])+"/receipts/agent") {
		t.Fatal("another port reused the first callback origin")
	}
	for _, request := range []struct{ method, path string }{{"HEAD", "/api/download?file=notes.txt"}, {"GET", "/app.js"}, {"GET", "/missing"}} {
		before := count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", run)
		join := "?"
		if strings.Contains(request.path, "?") {
			join = "&"
		}
		b.request(base, request.path+join+"run_id="+run, request.method, nil)
		collect(base, "/receipts/agent", secondToken, 403)
		if count(b.app.store.db, "SELECT COUNT(*) FROM events WHERE session_id=? AND kind='delivery'", run) != before {
			t.Fatal("HEAD/static/missing path counted as prompt delivery")
		}
		fresh := b.request(base, "/api/download?file=notes.txt&run_id="+run, "GET", nil)
		secondToken = responseToken(t, str(fresh.doc()["notes"]))
	}
	b.restart()
	collect(base, "/receipts/agent", secondToken, 201)
	collect(base, "/receipts/agent", firstToken, 403)
	workspace["callback_path"] = "/reports/new"
	workspace = b.api("/workspaces", "POST", workspace, 200)
	collect(base, "/receipts/agent", secondToken, 404)
	collect(base, "/collect", secondToken, 404)
	fresh := b.request(base, "/api/download?file=notes.txt&run_id="+run, "GET", nil)
	latest := responseToken(t, str(fresh.doc()["notes"]))
	if !strings.Contains(str(fresh.doc()["notes"]), "URL="+base+"/reports/new") {
		t.Fatal("saved callback not hot-updated for existing session")
	}
	collect(base, "/reports/new", latest, 201)
	collect(base, "/reports/new", secondToken, 403)
	if b.app.store.sessionView(run, 1)["current_callback_url"] != base+"/reports/new" {
		t.Fatal("session UI metadata still shows the old callback")
	}
	after := b.app.store.session(run)
	if dump(after["snapshot"]) != snapshot || !strings.HasPrefix(dump(after["events"]), strings.TrimSuffix(oldEvents, "]")) {
		t.Fatal("rotation or callback changes rewrote history")
	}
	current := rows(b.app.store.db, "SELECT token_hash FROM session_tokens WHERE session_id=?", run)
	if len(current) != 1 || current[0]["token_hash"] != tokenHash(latest) || current[0]["token_hash"] == latest {
		t.Fatal("latest authentication state missing or stored raw")
	}
	b.app.store.clear()
	if count(b.app.store.db, "SELECT COUNT(*) FROM session_tokens") != 0 {
		t.Fatal("token state outlived deleted sessions")
	}
}

func TestConcurrentRequestsKeepOnlyLastIssuedToken(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	deployment := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	l := b.listener(deployment)
	first, _ := b.app.store.composerSession(httptest.NewRequest("GET", "/", nil), l)
	run := str(first["id"])
	history := dump(first["snapshot"])
	tokens := make(chan string, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, _ := b.app.store.composerSession(httptest.NewRequest("GET", "/?run_id="+run, nil), l)
			tokens <- str(object(session["snapshot"])["token"])
		}()
	}
	wg.Wait()
	close(tokens)
	seen, accepted := map[string]bool{}, 0
	b.app.store.write(func(q queryer) {
		for token := range tokens {
			seen[token] = true
			if validSessionToken(q, run, object(first["snapshot"]), token) {
				accepted++
			}
		}
	})
	if len(seen) != 8 || accepted != 1 || dump(b.app.store.session(run)["snapshot"]) != history {
		t.Fatal("concurrent rotation did not preserve unique tokens, one current token and immutable history")
	}
}

func TestCallbackValidationAndScopedAgentEdits(t *testing.T) {
	a := editingAgent(t)
	s := a.manager.store
	site := s.saveComposer("sites", siteFixture())
	scenario := s.saveComposer("scenarios", object(generationDraft()["scenario"]))
	workspace := s.saveComposer("workspaces", Doc{"name": "Target", "slug": "target", "site_id": site["id"], "bindings": []any{Doc{"scenario_id": scenario["id"], "profile_id": "receipt"}}})
	other := s.saveComposer("workspaces", Doc{"name": "Other", "slug": "other"})
	a.workspaceID = str(workspace["id"])
	a.editScope = generationEditScope(Doc{"edit_scope": "scenario"}, Doc{})
	read := agentOperation(t, a, "read_callback", Doc{})
	if _, err := a.perform(context.Background(), "set_callback_path", Doc{"path": "/reports", "version": read["version"]}); err == nil {
		t.Fatal("scenario-only agent changed callback settings")
	}
	if _, err := a.perform(context.Background(), "set_callback_response", Doc{"json": `{"enabled":true}`, "version": read["version"]}); err == nil {
		t.Fatal("scenario-only agent changed rejection response")
	}
	a.editScope = generationEditScope(Doc{"edit_scope": "callback"}, Doc{})
	for _, path := range []string{"/", "/health", "/__am/preview", "https://example.com/collect", "/x?token=a", "/../x", "/a%2fb", "/index.html", "/api/download"} {
		if _, err := a.perform(context.Background(), "set_callback_path", Doc{"path": path, "version": read["version"]}); err == nil {
			t.Fatal("accepted invalid/conflicting callback: " + path)
		}
	}
	result := agentOperation(t, a, "set_callback_path", Doc{"path": "/reports", "version": read["version"]})
	if result["callback_path"] != "/reports" || get(s.db, "workspaces", str(other["id"]))["callback_path"] != "/collect" {
		t.Fatal("wrong workspace changed")
	}
	if get(s.db, "workspaces", a.workspaceID)["deployment_id"] != nil {
		t.Fatal("callback tool automatically published a workspace")
	}
	result = agentOperation(t, a, "set_callback_response", Doc{"version": result["version"], "json": `{"enabled":true,"status":409,"body":"{\"error\":\"EXPIRED\"}"}`})
	if integer(object(result["collect_rejected_response"])["status"]) != 409 || boolean(collectRejectedResponseConfig(get(s.db, "workspaces", str(other["id"])))["enabled"]) {
		t.Fatal("Agent rejection edit was not limited to current workspace")
	}
	current := get(s.db, "workspaces", a.workspaceID)
	current["name"] = "Concurrent edit"
	s.saveComposer("workspaces", current)
	if _, err := a.perform(context.Background(), "set_callback_path", Doc{"path": "/overwrite", "version": result["version"]}); err == nil {
		t.Fatal("callback tool overwrote a concurrent update")
	}
}
