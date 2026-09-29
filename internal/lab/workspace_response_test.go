package lab

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkspaceServerHeaderServedAcrossRoutesAndHotUpdates(t *testing.T) {
	b := newLab(t)
	_, scenario, workspace := composerFixture(b)
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	base := str(b.listener(dep)["public_url"])
	first := b.request(base, "/", "GET", nil)
	if _, exists := first.header["Server"]; exists {
		t.Fatal("unconfigured workspace emitted a server fingerprint")
	}
	run := first.header.Get("X-Run-ID")
	snapshot := dump(b.app.store.session(run)["snapshot"])
	release := dump(get(b.app.store.db, "composer_releases", str(dep["release_id"])))
	workspace["server_header"] = "nginx"
	workspace = b.api("/workspaces", "POST", workspace, 200)
	other := b.api("/workspaces", "POST", Doc{"name": "Other server", "slug": "other-server", "site_id": workspace["site_id"], "bindings": workspace["bindings"], "server_header": "Apache"}, 200)
	otherDep := b.api("/workspaces/"+str(other["id"])+"/publish", "POST", Doc{}, 200)
	otherBase := str(b.listener(otherDep)["public_url"])
	for _, tc := range []struct {
		method, path string
		body         any
		status       int
	}{
		{"GET", "/", nil, 200}, {"GET", "/app.js", nil, 200}, {"HEAD", "/", nil, 200},
		{"GET", "/api/download?file=notes.txt", nil, 200}, {"GET", "/api/download?file=other", nil, 403},
		{"GET", "/missing", nil, 404}, {"GET", "/health", nil, 200}, {"POST", "/collect", Doc{}, 400},
		{"POST", "/collect", Doc{"run_id": run, "token": "INVALID", "data": "example"}, 403},
	} {
		r := b.request(base, tc.path, tc.method, tc.body)
		if r.header.Get("Server") != "nginx" || r.status != tc.status {
			t.Fatalf("%s %s: status=%d, server=%q", tc.method, tc.path, r.status, r.header.Get("Server"))
		}
		if tc.method == "HEAD" && len(r.body) != 0 {
			t.Fatal("HEAD included body")
		}
	}
	profile := get(b.app.store.db, "profiles", str(object(array(workspace["bindings"])[0])["profile_id"]))
	profile["body"] = "{{token}}"
	b.api("/profiles", "POST", profile, 200)
	token := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	accepted := b.request(base, "/collect", "POST", Doc{"run_id": token.header.Get("X-Run-ID"), "token": token.doc()["notes"], "data": "example"})
	if accepted.status != 201 || accepted.header.Get("Server") != "nginx" {
		t.Fatalf("callback status=%d server=%q body=%s", accepted.status, accepted.header.Get("Server"), accepted.body)
	}
	if dump(b.app.store.session(run)["snapshot"]) != snapshot || dump(get(b.app.store.db, "composer_releases", str(dep["release_id"]))) != release {
		t.Fatal("header update rewrote historical snapshots")
	}
	preview := b.api("/composer/preview", "POST", Doc{"site_id": workspace["site_id"], "bindings": workspace["bindings"], "server_header": "nginx", "request": Doc{"path": "/", "method": "GET"}}, 200)
	if object(preview["headers"])["Server"] != "nginx" {
		t.Fatal("preview disagreed with live header")
	}
	// Explicit per-rule headers override the workspace default in all consumers.
	object(object(array(scenario["rules"])[0])["response"])["headers"] = Doc{"sErVeR": "Apache"}
	b.api("/scenarios", "POST", scenario, 200)
	if r := b.request(base, "/api/download?file=notes.txt", "GET", nil); r.header.Get("Server") != "Apache" {
		t.Fatal("rule header ignored")
	}
	preview = b.api("/composer/preview", "POST", Doc{"bindings": workspace["bindings"], "server_header": "nginx", "request": Doc{"path": "/api/download?file=notes.txt"}}, 200)
	if object(preview["headers"])["Server"] != "Apache" {
		t.Fatal("preview replaced rule override")
	}
	workspace["server_header"] = "Caddy"
	workspace = b.api("/workspaces", "POST", workspace, 200)
	if r := b.request(base, "/", "GET", nil); r.header.Get("Server") != "Caddy" || r.header.Get("X-Run-ID") != run {
		t.Fatal("existing session did not hot-update")
	}
	b.client.Jar, _ = cookiejar.New(nil)
	if r := b.request(base, "/", "GET", nil); r.header.Get("Server") != "Caddy" {
		t.Fatal("new session did not update")
	}
	b.restart()
	if r := b.request(base, "/", "GET", nil); r.header.Get("Server") != "Caddy" {
		t.Fatal("restart lost setting")
	}
	workspace = b.api("/workspaces/"+str(workspace["id"]), "GET", nil, 200)
	workspace["server_header"] = ""
	b.api("/workspaces", "POST", workspace, 200)
	if r := b.request(otherBase, "/", "GET", nil); r.header.Get("Server") != "Apache" {
		t.Fatal("setting leaked to another workspace")
	}
	if r := b.request(base, "/", "GET", nil); len(r.header.Values("Server")) != 0 {
		t.Fatal("empty header still emitted")
	}
}

func TestWorkspaceServerHeaderValidationAndAgentScope(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	a := bindingAgent(t, b, workspace)
	a.editScope = generationEditScope(Doc{"edit_scope": "site"}, Doc{})
	args := Doc{"server_header": "nginx", "version": workspace["version"]}
	if _, err := a.perform(context.Background(), "set_server_header", args); err == nil {
		t.Fatal("write without reading")
	}
	state := agentOperation(t, a, "read_response_settings", Doc{})
	args["version"] = state["version"]
	saved := agentOperation(t, a, "set_server_header", args)
	if saved["server_header"] != "nginx" || !boolean(saved["changed"]) {
		t.Fatal("header not saved")
	}
	args["version"] = saved["version"]
	unchanged := agentOperation(t, a, "set_server_header", args)
	if boolean(unchanged["changed"]) || integer(unchanged["version"]) != integer(saved["version"]) {
		t.Fatal("no-op saved again")
	}
	before := dump(get(b.app.store.db, "workspaces", str(workspace["id"])))
	for _, value := range []any{"nginx\r\nX-Fake: injected", "\nnginx", "nginx\t", "中", "{{token}}", strings.Repeat("x", 201), nil, Doc{}} {
		raw := get(b.app.store.db, "workspaces", str(workspace["id"]))
		raw["server_header"] = value
		b.api("/workspaces", "POST", raw, 400)
		if dump(get(b.app.store.db, "workspaces", str(workspace["id"]))) != before {
			t.Fatal("invalid header partially saved")
		}
	}
	for _, scope := range []string{"bindings", "scenario", "profile", "callback", "read_only"} {
		a.editScope = generationEditScope(Doc{"edit_scope": scope}, Doc{})
		if _, err := a.perform(context.Background(), "set_server_header", args); err == nil || !strings.Contains(err.Error(), "本轮修改范围") {
			t.Fatal("scope bypass", scope)
		}
	}
	a.editScope = generationEditScope(Doc{"edit_scope": "site"}, Doc{})
	a.agentConfig = Doc{"tools": []any{"read_response_settings"}}
	if _, err := a.perform(context.Background(), "set_server_header", args); err == nil {
		t.Fatal("unbound tool executed")
	}
	a.agentConfig = nil
	current := get(b.app.store.db, "workspaces", str(workspace["id"]))
	current["name"] = "Concurrent edit"
	b.api("/workspaces", "POST", current, 200)
	args["server_header"] = "Apache"
	if _, err := a.perform(context.Background(), "set_server_header", args); err == nil {
		t.Fatal("stale version overwritten")
	}
	state = agentOperation(t, a, "read_response_settings", Doc{})
	args["version"] = state["version"]
	agentOperation(t, a, "set_server_header", args)
	current = get(b.app.store.db, "workspaces", str(workspace["id"]))
	delete(current, "server_header") // Clients predating this field preserve it.
	current = b.api("/workspaces", "POST", current, 200)
	if current["server_header"] != "Apache" || current["name"] != "Concurrent edit" {
		t.Fatal("older client dropped field or concurrent edit")
	}
	for _, prompt := range []string{"修改 Server 头为 nginx", "修改Server响应头为 Apache", "设置server_header为 Caddy"} {
		if dump(generationEditScope(Doc{}, Doc{"prompt": prompt})["modules"]) != `["site"]` {
			t.Fatal("header scope not recognized", prompt)
		}
	}
}

func TestServerHeaderAgentDialogueAndIsolatedReview(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	base := str(b.listener(dep)["public_url"])
	var rounds atomic.Int32
	agentModel(t, b.app, func(w http.ResponseWriter, r *http.Request) {
		input := agentRequestBody(r)
		messages := array(input["messages"])
		switch rounds.Add(1) {
		case 1:
			if !strings.Contains(str(object(messages[0])["content"]), "read_response_settings") {
				t.Error("writer instructions omitted site response identity")
			}
			agentReply(w, agentToolCall("read", "read_response_settings", Doc{}))
		case 2:
			state := decode(str(object(messages[len(messages)-1])["content"]))
			agentReply(w, agentToolCall("set", "set_server_header", Doc{"version": state["version"], "server_header": "nginx"}))
		case 3:
			agentReply(w, agentToolCall("finish", "finish_draft", Doc{"summary": "Server 头已保存为 nginx。"}))
		default:
			t.Error("unexpected round")
		}
	})
	job := generationRequest(t, b.app, "POST", "jobs", Doc{"workspace_id": workspace["id"], "prompt": "修改 Server 头为 nginx"}, 202)
	done := generationWait(t, b.app, str(job["id"]))
	if done["status"] != "completed" || object(done["result"])["kind"] != "message" {
		t.Fatalf("unexpected result %s", dump(done))
	}
	if r := b.request(base, "/", "GET", nil); r.header.Get("Server") != "nginx" {
		t.Fatal("agent update not live")
	}
	snapshot := reviewSnapshot(b.app.store.db, str(workspace["id"]))
	reviewer := &workspaceReviewer{manager: b.app.generation, snapshot: snapshot}
	if err := reviewer.open(); err != nil {
		t.Fatal(err)
	}
	defer reviewer.sandbox.close()
	result := reviewer.sandbox.request(Doc{"path": "/", "method": "GET"}, true)
	if object(result["headers"])["Server"] != "nginx" {
		t.Fatal("review/redteam isolated response differs from deployed response")
	}
}

func TestServerHeaderToolMigrationKeepsUserChoices(t *testing.T) {
	app := generationTestApp(t)
	legacy := clone(get(app.store.db, "agent_definitions", "writer"))
	legacy["tools"] = []any{"finish_draft"}
	delete(legacy, "response_tools_version")
	app.store.write(func(q queryer) { put(q, "agent_definitions", legacy) })
	initAgentDefinitions(app.store)
	writer := get(app.store.db, "agent_definitions", "writer")
	if len(array(writer["tools"])) != 3 || !has(writer["tools"], "set_server_header") {
		t.Fatal("new header tools unavailable or disabled old tools enabled")
	}
	writer["tools"] = []any{"finish_draft"}
	writer = app.store.saveAgentDefinition(writer)
	initAgentDefinitions(app.store)
	if dump(get(app.store.db, "agent_definitions", "writer")) != dump(writer) {
		t.Fatal("restart enabled manually disabled header tools")
	}
}
