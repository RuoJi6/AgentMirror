package lab

import (
	"context"
	"strings"
	"testing"
)

func editingAgent(t *testing.T) *draftAgent {
	t.Helper()
	app := generationTestApp(t)
	app.generation.saveJob(Doc{"id": "editing", "status": "running", "events": []any{}})
	return &draftAgent{manager: app.generation, jobID: "editing"}
}

func agentOperation(t *testing.T, a *draftAgent, tool string, args Doc) Doc {
	t.Helper()
	d, err := a.perform(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return d
}

func TestIncrementalFileEditsPreserveWhitespaceAndRejectStaleHashes(t *testing.T) {
	a := editingAgent(t)
	first := agentOperation(t, a, "write_file", Doc{"path": "index.html", "content": "<h1>初始标题</h1>\n"})
	second := agentOperation(t, a, "append_file", Doc{"path": "index.html", "expected_sha256": first["sha256"], "content": "  <p>追加正文</p>\n"})
	page := agentOperation(t, a, "read_file", Doc{"path": "index.html"})
	if page["content"] != "<h1>初始标题</h1>\n  <p>追加正文</p>\n" || page["sha256"] != second["sha256"] {
		t.Fatal("append lost indentation or newline")
	}
	before := dump(a.site)
	if _, err := a.perform(context.Background(), "edit_file", Doc{"path": "index.html", "expected_sha256": first["sha256"], "old_text": "初始标题", "new_text": "更新标题"}); err == nil || dump(a.site) != before {
		t.Fatal("stale edit overwrote a newer file")
	}
	agentOperation(t, a, "edit_file", Doc{"path": "index.html", "expected_sha256": second["sha256"], "old_text": "  <p>追加正文</p>\n", "new_text": "    <p>更新正文</p>\n"})
	page = agentOperation(t, a, "read_file", Doc{"path": "index.html"})
	if !strings.HasSuffix(str(page["content"]), "    <p>更新正文</p>\n") {
		t.Fatal("exact edit lost whitespace")
	}
	file := agentOperation(t, a, "write_file", Doc{"path": "repeated.txt", "content": "repeat repeat"})
	before = dump(a.site)
	if _, err := a.perform(context.Background(), "edit_file", Doc{"path": "repeated.txt", "expected_sha256": file["sha256"], "old_text": "repeat", "new_text": "unique"}); err == nil || dump(a.site) != before {
		t.Fatal("ambiguous edit succeeded or changed data")
	}
	a.site["files"] = append(array(a.site["files"]), Doc{"path": "icon.png", "encoding": "base64", "content": "aWNvbg=="})
	if _, err := a.perform(context.Background(), "append_file", Doc{"path": "icon.png", "expected_sha256": "unused", "content": "text"}); err == nil {
		t.Fatal("binary resource edited as text")
	}
}

func TestSavedMaterialToolsEditSameRecordsAndEnforceVersions(t *testing.T) {
	a := editingAgent(t)
	s := a.manager.store
	site := s.saveComposer("sites", siteFixture())
	scenario := s.saveComposer("scenarios", object(generationDraft()["scenario"]))
	profile := s.save("profiles", Doc{"name": "Editable profile", "description": "Keep description", "category": "unexpected_output", "body": "ORIGINAL", "fields": []any{Doc{"name": "note", "type": "string", "required": false}}, "commands": []any{}})
	if _, err := a.perform(context.Background(), "save_material", Doc{"kind": "profile", "id": profile["id"], "version": 1, "json": `{"body":"BAD"}`}); err == nil {
		t.Fatal("write without read succeeded")
	}
	p := agentOperation(t, a, "read_material", Doc{"kind": "profile", "id": profile["id"]})
	if p["content"] != "ORIGINAL" {
		t.Fatal("explicit edit could not read body")
	}
	agentOperation(t, a, "save_material", Doc{"kind": "profile", "id": profile["id"], "version": 1, "json": `{"body":"UPDATED"}`})
	p = get(s.db, "profiles", str(profile["id"]))
	if p["body"] != "UPDATED" || p["description"] != "Keep description" || len(array(p["fields"])) != 1 || integer(p["version"]) != 2 {
		t.Fatal("partial profile update lost fields")
	}
	if count(s.db, "SELECT COUNT(*) FROM versions WHERE profile_id=?", profile["id"]) != 2 {
		t.Fatal("profile history missing")
	}
	// A second editor advances the version after this agent's read.
	p["body"] = "CONCURRENT"
	s.save("profiles", p)
	if _, err := a.perform(context.Background(), "save_material", Doc{"kind": "profile", "id": profile["id"], "version": 2, "json": `{"body":"STALE"}`}); err == nil {
		t.Fatal("concurrent change overwritten")
	}
	if _, err := a.perform(context.Background(), "delete_material", Doc{"kind": "profile", "id": profile["id"], "version": 2}); err == nil {
		t.Fatal("concurrent profile deleted")
	}
	siteCount := len(listing(s.db, "sites"))
	agentOperation(t, a, "load_material", Doc{"kind": "site", "id": site["id"]})
	file := agentOperation(t, a, "read_file", Doc{"path": "index.html"})
	agentOperation(t, a, "append_file", Doc{"path": "index.html", "expected_sha256": file["sha256"], "content": "\n<footer>Updated</footer>\n"})
	agentOperation(t, a, "save_material", Doc{"kind": "site", "id": site["id"], "version": site["version"]})
	if len(listing(s.db, "sites")) != siteCount || integer(get(s.db, "sites", str(site["id"]))["version"]) != 2 || a.result()["kind"] != "message" {
		t.Fatal("saved edit created duplicate or adoptable duplicate draft")
	}
	agentOperation(t, a, "load_material", Doc{"kind": "scenario", "id": scenario["id"]})
	rule := agentOperation(t, a, "read_scenario", Doc{"rule_id": "download"})
	rule["path"] = "/changed"
	agentOperation(t, a, "upsert_rule", Doc{"rule_id": "download", "rule": rule})
	rule["path"] = "/second"
	agentOperation(t, a, "upsert_rule", Doc{"rule_id": "second", "rule": rule})
	agentOperation(t, a, "delete_rule", Doc{"rule_id": "second"})
	agentOperation(t, a, "save_material", Doc{"kind": "scenario", "id": scenario["id"], "version": scenario["version"]})
	if object(array(get(s.db, "scenarios", str(scenario["id"]))["rules"])[0])["path"] != "/changed" {
		t.Fatal("incremental rule edit not saved")
	}
	workspace := s.saveComposer("workspaces", Doc{"name": "Bound", "slug": "bound", "site_id": site["id"], "bindings": []any{Doc{"scenario_id": scenario["id"], "profile_id": profile["id"]}}})
	if _, err := a.perform(context.Background(), "delete_material", Doc{"kind": "scenario", "id": scenario["id"], "version": 2}); err == nil {
		t.Fatal("referenced scenario deleted")
	}
	workspace["bindings"] = []any{}
	s.saveComposer("workspaces", workspace)
	agentOperation(t, a, "delete_material", Doc{"kind": "scenario", "id": scenario["id"], "version": 2})
	if entityMaybe(s.db, "scenarios", str(scenario["id"])) != nil || entityMaybe(s.db, "scenarios_versions", str(scenario["id"])+"-v2") == nil {
		t.Fatal("delete missing or removed history")
	}
}

func TestAgentContextCompactsWholeExchanges(t *testing.T) {
	a := &draftAgent{site: siteFixture(), materialActions: []any{Doc{"action": "saved", "id": "record"}}}
	last := Doc{"role": "assistant", "content": nil, "tool_calls": []any{agentToolCall("last", "read_file", Doc{"path": "index.html"})}, "_provider_content": []any{Doc{"type": "thinking", "thinking": "opaque", "signature": "signature"}}}
	messages := []Doc{{"role": "system", "content": "instructions"}, {"role": "user", "content": "CURRENT_REQUEST"}, {"role": "assistant", "content": strings.Repeat("OLD_CONTENT", 30000)}, {"role": "tool", "tool_call_id": "old", "content": "old result"}, last, {"role": "tool", "tool_call_id": "last", "content": "latest result"}}
	result := compactAgentContext(messages, 1, a)
	if len(jsonBytes(result)) > 16000 || !strings.Contains(dump(result), "CURRENT_REQUEST") || strings.Contains(dump(result), "OLD_CONTENT") || dump(result[len(result)-2]) != dump(last) || result[len(result)-1]["tool_call_id"] != "last" {
		t.Fatal("compaction lost request, split exchange or changed signed block")
	}
	if !strings.Contains(dump(result), "saved_actions") {
		t.Fatal("compaction forgot completed saved changes")
	}
}
