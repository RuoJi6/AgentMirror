package lab

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync/atomic"
	"testing"
)

func bindingAgent(t *testing.T, b *testLab, workspace Doc) *draftAgent {
	t.Helper()
	m := b.app.generationManager()
	id := randomHex(6)
	m.saveJob(Doc{"id": id, "status": "running", "events": []any{}})
	return &draftAgent{manager: m, jobID: id, workspaceID: str(workspace["id"]), editScope: generationEditScope(Doc{"edit_scope": "bindings"}, Doc{})}
}

func TestBindingAgentUpdatesOnlyRequestedAssignments(t *testing.T) {
	b := newLab(t)
	site, scenario, workspace := composerFixture(b)
	profile := b.api("/profiles", "POST", Doc{"name": "Alternative", "body": "ALTERNATIVE_BODY", "category": "unexpected_output"}, 200)
	oldID := object(array(workspace["bindings"])[0])["profile_id"]
	second := clone(object(array(workspace["bindings"])[0]))
	second["id"], second["paths"] = "instance-b", Doc{"download": "/second"}
	workspace["bindings"] = append(array(workspace["bindings"]), second)
	workspace = b.api("/workspaces", "POST", workspace, 200)
	second = clone(object(array(workspace["bindings"])[1]))
	other := clone(workspace)
	delete(other, "id")
	other["name"], other["slug"] = "Other", "other"
	other = b.api("/workspaces", "POST", other, 200)
	beforeOther := dump(get(b.app.store.db, "workspaces", str(other["id"])))
	beforeMaterials := dump(Doc{"sites": listing(b.app.store.db, "sites"), "scenarios": listing(b.app.store.db, "scenarios"), "profiles": listing(b.app.store.db, "profiles")})
	a := bindingAgent(t, b, workspace)
	read := agentOperation(t, a, "read_bindings", Doc{})
	if strings.Contains(dump(read), "ALTERNATIVE_BODY") || strings.Contains(dump(read), "CANARY original") {
		t.Fatal("binding read exposed prompt text")
	}
	change := func(name string, args Doc) Doc {
		args["version"] = a.bindingVersion
		return agentOperation(t, a, name, args)
	}
	change("set_binding_profile", Doc{"binding_id": "instance-a", "rule_id": "download", "profile_id": profile["id"]})
	change("set_binding_profile", Doc{"binding_id": "instance-a", "profile_id": profile["id"]})
	result := change("set_binding_profile", Doc{"binding_id": "instance-a", "profile_id": oldID})
	binding := object(array(result["bindings"])[0])
	if binding["profile_id"] != oldID || object(binding["rule_profiles"])["download"] != profile["id"] {
		t.Fatal("default switch lost rule override")
	}
	result = change("set_binding_profile", Doc{"binding_id": "instance-a", "rule_id": "download", "profile_id": ""})
	rule := object(array(object(array(result["bindings"])[0])["rules"])[0])
	if rule["profile_id"] != oldID || !boolean(rule["inherits_default"]) {
		t.Fatal("clearing override did not restore inheritance")
	}
	version := a.bindingVersion
	result = change("set_binding_profile", Doc{"binding_id": "instance-a", "profile_id": oldID})
	if boolean(result["changed"]) || a.bindingVersion != version {
		t.Fatal("no-op created duplicate save")
	}
	result = change("upsert_binding", Doc{"binding_id": "instance-a", "json": `{"paths":{"download":"/mapped"},"enabled":false}`})
	if boolean(object(array(result["bindings"])[0])["enabled"]) {
		t.Fatal("disable failed")
	}
	change("upsert_binding", Doc{"binding_id": "instance-a", "json": `{"paths":{"download":""},"enabled":true}`})
	result = change("upsert_binding", Doc{"json": dump(Doc{"scenario_id": scenario["id"], "profile_id": profile["id"], "paths": Doc{"download": "/third"}})})
	newID := result["binding_id"]
	result = change("move_binding", Doc{"binding_id": newID, "position": 1})
	if object(array(result["bindings"])[0])["id"] != newID {
		t.Fatal("move failed")
	}
	replacement := b.api("/scenarios", "POST", Doc{"name": "Replacement", "rules": []any{Doc{"id": "info", "method": "GET", "path": "/info", "delivery_required": false, "response": Doc{"status": 200, "format": "text", "body": "OK"}}}}, 200)
	result = change("upsert_binding", Doc{"binding_id": newID, "json": dump(Doc{"scenario_id": replacement["id"]})})
	replaced := object(array(result["bindings"])[0])
	if len(object(replaced["paths"])) != 0 || replaced["profile_id"] != profile["id"] {
		t.Fatal("scenario switch retained invalid overrides or lost default")
	}
	change("delete_binding", Doc{"binding_id": newID})
	saved := get(b.app.store.db, "workspaces", str(workspace["id"]))
	if len(array(saved["bindings"])) != 2 || dump(object(array(saved["bindings"])[1])) != dump(second) {
		t.Fatal("unrequested instance changed")
	}
	if entityMaybe(b.app.store.db, "scenarios", str(replacement["id"])) == nil {
		t.Fatal("removing an instance deleted its shared scenario")
	}

	// Remove only the separately created test scenario from this comparison.
	materials := listing(b.app.store.db, "scenarios")
	kept := []any{}
	for _, raw := range materials {
		if object(raw)["id"] != replacement["id"] {
			kept = append(kept, raw)
		}
	}
	if dump(Doc{"sites": listing(b.app.store.db, "sites"), "scenarios": kept, "profiles": listing(b.app.store.db, "profiles")}) != beforeMaterials || dump(get(b.app.store.db, "workspaces", str(other["id"]))) != beforeOther {
		t.Fatal("binding operation changed shared materials or another workspace")
	}
	if saved["site_id"] != site["id"] || str(saved["deployment_id"]) != "" || len(a.materialActions) == 0 {
		t.Fatal("binding operation changed site, published unexpectedly or omitted events")
	}
}

func TestBindingAgentRejectsStaleInvalidAndOutOfScopeWrites(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	a := bindingAgent(t, b, workspace)
	base := Doc{"binding_id": "instance-a", "profile_id": "", "version": workspace["version"]}
	if _, err := a.perform(context.Background(), "set_binding_profile", base); err == nil {
		t.Fatal("write before read")
	}
	agentOperation(t, a, "read_bindings", Doc{})
	before := dump(get(b.app.store.db, "workspaces", str(workspace["id"])))
	for _, tc := range []struct {
		name string
		args Doc
	}{
		{"set_binding_profile", Doc{"binding_id": "foreign", "profile_id": ""}},
		{"set_binding_profile", Doc{"binding_id": "instance-a", "profile_id": "missing"}},
		{"set_binding_profile", Doc{"binding_id": "instance-a", "rule_id": "missing", "profile_id": ""}},
		{"set_binding_profile", Doc{"binding_id": "instance-a", "profile_id": ""}},
		{"upsert_binding", Doc{"binding_id": "instance-a", "json": `{"paths":{"download":"/collect"}}`}},
		{"upsert_binding", Doc{"binding_id": "instance-a", "json": `{"scenario_id":{}}`}},
		{"upsert_binding", Doc{"binding_id": "instance-a", "json": `{"site_id":"wrong"}`}},
		{"upsert_binding", Doc{"json": `{}`}},
		{"move_binding", Doc{"binding_id": "instance-a", "position": 0}},
	} {
		tc.args["version"] = a.bindingVersion
		if _, err := a.perform(context.Background(), tc.name, tc.args); err == nil {
			t.Fatalf("accepted invalid %s %s", tc.name, dump(tc.args))
		}
		if dump(get(b.app.store.db, "workspaces", str(workspace["id"]))) != before || len(a.materialActions) != 0 {
			t.Fatal("failed operation partially saved")
		}
	}
	for _, scope := range []string{"scenario", "profile", "site", "callback", "read_only"} {
		a.editScope = generationEditScope(Doc{"edit_scope": scope}, Doc{})
		for _, name := range []string{"set_binding_profile", "upsert_binding", "delete_binding", "move_binding"} {
			if _, err := a.perform(context.Background(), name, base); err == nil || !strings.Contains(err.Error(), "本轮修改范围") {
				t.Fatalf("scope bypass %s %s: %v", scope, name, err)
			}
		}
	}
	a.editScope = generationEditScope(Doc{"edit_scope": "bindings"}, Doc{})
	for _, tool := range a.scopedTools() {
		if k := toolEditModule(str(object(tool["function"])["name"])); k != "" && k != "bindings" {
			t.Fatal("other module write tool advertised")
		}
	}
	if _, err := a.perform(context.Background(), "save_material", Doc{"kind": "profile", "json": `{"body":"BAD"}`}); err == nil {
		t.Fatal("binding scope permitted prompt text edits")
	}
	a.agentConfig = Doc{"tools": []any{"read_bindings"}}
	if _, err := a.perform(context.Background(), "set_binding_profile", base); err == nil || !strings.Contains(err.Error(), "未绑定工具") {
		t.Fatal("disabled tool executed")
	}
	a.agentConfig = nil
	workspace["name"] = "Changed concurrently"
	workspace = b.api("/workspaces", "POST", workspace, 200)
	if _, err := a.perform(context.Background(), "delete_binding", Doc{"version": a.bindingVersion, "binding_id": "instance-a"}); err == nil {
		t.Fatal("concurrent edit overwritten")
	}
	agentOperation(t, a, "read_bindings", Doc{})
	agentOperation(t, a, "delete_binding", Doc{"version": a.bindingVersion, "binding_id": "instance-a"})
	if get(b.app.store.db, "workspaces", str(workspace["id"]))["name"] != "Changed concurrently" {
		t.Fatal("reread lost concurrent edit")
	}
}

func TestBindingAgentChatHotUpdatesLiveSessionsWithoutAdoption(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	profile := b.api("/profiles", "POST", Doc{"name": "Switch target", "body": "NEW_BINDING {{run_id}}", "category": "unexpected_output"}, 200)
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{}, 200)
	base := str(b.listener(dep)["public_url"])
	first := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	run := first.header.Get("X-Run-ID")
	history := b.app.store.session(run)
	release := dump(get(b.app.store.db, "composer_releases", str(dep["release_id"])))
	var rounds atomic.Int32
	agentModel(t, b.app, func(w http.ResponseWriter, r *http.Request) {
		input := agentRequestBody(r)
		messages := array(input["messages"])
		switch rounds.Add(1) {
		case 1:
			found := false
			for _, raw := range array(input["tools"]) {
				if object(object(raw)["function"])["name"] == "set_binding_profile" {
					found = true
				}
			}
			if !found {
				t.Error("configured writer did not receive binding tools")
			}
			agentReply(w, agentToolCall("read", "read_bindings", Doc{}))
		case 2:
			state := decode(str(object(messages[len(messages)-1])["content"]))
			agentReply(w, agentToolCall("switch", "set_binding_profile", Doc{"version": state["version"], "binding_id": "instance-a", "profile_id": profile["id"]}))
		case 3:
			state := decode(str(object(messages[len(messages)-1])["content"]))
			if !boolean(state["changed"]) || str(state["live_update"]) == "" {
				t.Error("save not acknowledged")
			}
			agentReply(w, agentToolCall("finish", "finish_draft", Doc{"summary": "绑定已切换并保存。"}))
		default:
			t.Error("unexpected model round")
		}
	})
	job := generationRequest(t, b.app, "POST", "jobs", Doc{"workspace_id": workspace["id"], "prompt": "将场景默认提示词改为 Switch target"}, 202)
	done := generationWait(t, b.app, str(job["id"]))
	if done["status"] != "completed" || object(done["result"])["kind"] != "message" || dump(object(done["edit_scope"])["modules"]) != `["bindings"]` {
		t.Fatalf("unexpected result: %s", dump(done))
	}
	current := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	if str(current.doc()["notes"]) != "NEW_BINDING "+run || current.header.Get("X-Run-ID") != run {
		t.Fatal("existing session not switched")
	}
	b.client.Jar, _ = cookiejar.New(nil)
	fresh := b.request(base, "/api/download?file=notes.txt", "GET", nil)
	if !strings.HasPrefix(str(fresh.doc()["notes"]), "NEW_BINDING ") || fresh.header.Get("X-Run-ID") == run {
		t.Fatal("new session not switched")
	}
	after := b.app.store.session(run)
	if dump(after["snapshot"]) != dump(history["snapshot"]) || dump(get(b.app.store.db, "composer_releases", str(dep["release_id"]))) != release {
		t.Fatal("historical snapshot changed")
	}
	for i, event := range array(history["events"]) {
		if dump(event) != dump(array(after["events"])[i]) {
			t.Fatal("historical delivery changed")
		}
	}
	b.restart()
	if r := b.request(base, "/api/download?file=notes.txt&run_id="+run, "GET", nil); str(r.doc()["notes"]) != "NEW_BINDING "+run {
		t.Fatal("saved binding lost after restart")
	}
}

func TestBindingToolMigrationPreservesConfiguredPermissions(t *testing.T) {
	app := generationTestApp(t)
	s := app.store
	legacy := clone(get(s.db, "agent_definitions", "writer"))
	legacy["tools"] = []any{"read_file", "finish_draft"}
	legacy["instructions"] = "User instructions"
	delete(legacy, "binding_tools_version")
	custom := clone(legacy)
	custom["id"], custom["builtin"] = "custom-writer", false
	s.write(func(q queryer) { put(q, "agent_definitions", legacy); put(q, "agent_definitions", custom) })
	initAgentDefinitions(s)
	current := get(s.db, "agent_definitions", "writer")
	if len(array(current["tools"])) != 7 || current["instructions"] != legacy["instructions"] || has(current["tools"], "write_file") || dump(get(s.db, "agent_definitions", "custom-writer")) != dump(custom) {
		t.Fatal("migration changed existing permissions/settings")
	}
	before := dump(current)
	initAgentDefinitions(s)
	if dump(get(s.db, "agent_definitions", "writer")) != before {
		t.Fatal("migration repeated")
	}
	current["tools"] = []any{"read_file", "finish_draft"}
	current = s.saveAgentDefinition(current)
	initAgentDefinitions(s)
	if dump(get(s.db, "agent_definitions", "writer")) != dump(current) || has(current["tools"], "set_binding_profile") {
		t.Fatal("restart re-enabled manually disabled tools")
	}
}
