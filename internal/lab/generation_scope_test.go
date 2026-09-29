package lab

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGenerationEditScopeUsesCurrentIntentNotContext(t *testing.T) {
	refs := []any{Doc{"kind": "scenario", "id": "chosen"}}
	for _, tc := range []struct {
		name, prompt, selection, want string
		refs                          []any
	}{
		{"screenshot", "修改：删除多余字段，需要真实", "", `["scenario"]`, refs},
		{"site is context", "只修改模拟场景，不要修改站点", "auto", `["scenario"]`, []any{Doc{"kind": "site"}}},
		{"profile is input", "根据这个提示词，创建一个堡垒机的场景", "", `["scenario"]`, []any{Doc{"kind": "profile"}}},
		{"callback is target", "修改当前工作区的回传路径为 /agent/receipt", "", `["callback"]`, refs},
		{"joined targets", "修改站点和模拟场景", "auto", `["site","scenario"]`, refs},
		{"separate targets", "修改页面；编辑提示词", "auto", `["site","profile"]`, refs},
		{"binding after target", "把这个接口的提示词切换为 A", "", `["bindings"]`, refs},
		{"binding default", "将场景默认提示词改为 A", "", `["bindings"]`, refs},
		{"binding override", "绑定提示词", "", `["bindings"]`, []any{Doc{"kind": "profile"}}},
		{"binding inheritance", "恢复接口继承默认提示词", "", `["bindings"]`, refs},
		{"binding instance", "停用场景实例", "", `["bindings"]`, refs},
		{"binding mapping", "修改接口映射", "", `["bindings"]`, refs},
		{"switch scenario instance", "切换模拟场景为场景 B", "", `["bindings"]`, refs},
		{"switch instance prompt", "将这个场景的提示词改成 B", "", `["bindings"]`, refs},
		{"move instance", "把实例 2 移到最前", "", `["bindings"]`, refs},
		{"binding add", "添加场景到工作区", "", `["bindings"]`, refs},
		{"body is not assignment", "修改提示词正文的绑定说明", "", `["profile"]`, refs},
		{"explicit boundary wins", "修改站点和模拟场景", "scenario", `["scenario"]`, refs},
		{"explicit all", "修改指定内容", "all", `["site","scenario","profile","callback","bindings","ports"]`, refs},
		{"read only", "解释场景", "read_only", `[]`, refs},
		{"new request not inherited", "继续解释", "", `["site","scenario","profile","callback","bindings","ports"]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := Doc{"prompt": tc.prompt, "edit_scope": tc.selection, "references": tc.refs}
			input := Doc{"prompt": tc.prompt, "site": siteFixture(), "scenario": generationDraft()["scenario"], "references": refs}
			scope := generationEditScope(raw, input)
			if dump(scope["modules"]) != tc.want {
				t.Fatalf("scope = %s, want %s", dump(scope), tc.want)
			}
		})
	}
}

func TestAgentScopeRejectsEveryOutOfScopeMutationAtomically(t *testing.T) {
	a := editingAgent(t)
	a.site = siteFixture()
	a.scenario = object(generationDraft()["scenario"])
	a.editScope = generationEditScope(Doc{"edit_scope": "scenario"}, Doc{})
	beforeSite, beforeScenario := dump(a.site), dump(a.scenario)
	for _, tc := range []struct {
		name string
		args Doc
	}{
		{"write_file", Doc{"path": "index.html", "content": "wrong"}},
		{"append_file", Doc{"path": "index.html", "content": "wrong"}},
		{"edit_file", Doc{"path": "index.html", "old_text": "old", "new_text": "wrong"}},
		{"delete_file", Doc{"path": "index.html"}},
		{"set_site", Doc{"name": "wrong", "entry": "index.html"}},
		{"clone_website", Doc{"url": "https://example.com"}},
		{"use_reference", Doc{"kind": "site", "id": "wrong"}},
		{"load_material", Doc{"kind": "site", "id": "wrong"}},
		{"save_material", Doc{"kind": "site"}},
		{"save_material", Doc{"kind": "profile", "json": `{"body":"wrong"}`}},
		{"delete_material", Doc{"kind": "profile", "id": "wrong", "version": 1}},
		{"delete_material", Doc{"kind": "site", "id": "wrong", "version": 1}},
		{"replace_draft", Doc{"json": dump(generationDraft())}},
	} {
		t.Run(tc.name+str(tc.args["kind"]), func(t *testing.T) {
			if _, err := a.perform(context.Background(), tc.name, tc.args); err == nil || !strings.Contains(err.Error(), "本轮修改范围") {
				t.Fatalf("missing scope error: %v", err)
			}
			if dump(a.site) != beforeSite || dump(a.scenario) != beforeScenario || len(a.materialActions) != 0 {
				t.Fatal("rejected operation partially mutated state")
			}
		})
	}
	agentOperation(t, a, "read_file", Doc{"path": "index.html"})
	agentOperation(t, a, "set_scenario", Doc{"scenario": a.scenario})
	a.editScope = generationEditScope(Doc{"edit_scope": "read_only"}, Doc{})
	if _, err := a.perform(context.Background(), "set_scenario", Doc{"scenario": a.scenario}); err == nil {
		t.Fatal("read-only scope allowed a write")
	}
	a.editScope = generationEditScope(Doc{"edit_scope": "all"}, Doc{})
	agentOperation(t, a, "write_file", Doc{"path": "index.html", "content": "<h1>Allowed</h1>"})
}

func TestAgentScopeToolSchemasKeepReadsAndNarrowSharedWriters(t *testing.T) {
	a := &draftAgent{editScope: generationEditScope(Doc{"edit_scope": "scenario"}, Doc{})}
	names := map[string]Doc{}
	for _, tool := range a.scopedTools() {
		f := object(tool["function"])
		names[str(f["name"])] = f
	}
	for _, name := range []string{"write_file", "append_file", "edit_file", "delete_file", "set_site", "clone_website"} {
		if names[name] != nil {
			t.Fatal("unrelated write tool offered: " + name)
		}
	}
	for _, name := range []string{"read_file", "read_material", "list_materials", "read_reference", "set_scenario", "upsert_rule", "delete_rule"} {
		if names[name] == nil {
			t.Fatal("needed read or scenario tool missing: " + name)
		}
	}
	for _, name := range []string{"load_material", "use_reference", "save_material", "delete_material"} {
		k := object(object(object(names[name]["parameters"])["properties"])["kind"])
		if dump(k["enum"]) != `["scenario"]` {
			t.Fatal("generic writer not narrowed: " + name)
		}
	}
	readKinds := object(object(object(names["read_material"]["parameters"])["properties"])["kind"])
	if len(array(readKinds["enum"])) != 3 {
		t.Fatal("narrowing writes mutated shared read schema")
	}
}

// Reproduce the user's failure with a deliberately incorrect provider tool call.
// Even a provider that ignores the offered schema cannot rewrite index.html.
func TestScenarioReferenceRejectsSiteRewriteAndRecoversToSaveExactScenario(t *testing.T) {
	app := generationTestApp(t)
	site := app.store.saveComposer("sites", siteFixture())
	scenario := app.store.saveComposer("scenarios", object(generationDraft()["scenario"]))
	beforeSite := dump(get(app.store.db, "sites", str(site["id"])))
	rule := clone(object(array(scenario["rules"])[0]))
	rule["path"] = "/requested-change"
	var round atomic.Int32
	agentModel(t, app, func(w http.ResponseWriter, r *http.Request) {
		request := agentRequestBody(r)
		switch round.Add(1) {
		case 1:
			for _, value := range array(request["tools"]) {
				if object(object(value)["function"])["name"] == "write_file" {
					t.Error("wrong write tool offered")
				}
			}
			agentReply(w, agentToolCall("wrong", "write_file", Doc{"path": "index.html", "content": "WRONG MODULE"}))
		case 2:
			if !strings.Contains(dump(request["messages"]), "本轮修改范围") {
				t.Error("model did not receive scope feedback")
			}
			agentReply(w, agentToolCall("load", "load_material", Doc{"kind": "scenario", "id": scenario["id"]}))
		case 3:
			agentReply(w, agentToolCall("rule", "upsert_rule", Doc{"rule_id": rule["id"], "rule": rule}))
		case 4:
			agentReply(w, agentToolCall("save", "save_material", Doc{"kind": "scenario", "id": scenario["id"], "version": scenario["version"]}))
		case 5:
			generationEnvelope(w, "已更新选中的场景规则。")
		default:
			t.Error("unexpected model round")
			generationEnvelope(w, "停止")
		}
	})
	job := generationRequest(t, app, "POST", "jobs", Doc{"prompt": "修改：调整所选规则", "references": []any{Doc{"kind": "scenario", "id": scenario["id"]}}, "site": site, "scenario": scenario}, 202)
	job = generationWait(t, app, str(job["id"]))
	result := object(job["result"])
	if job["status"] != "completed" || dump(object(job["edit_scope"])["modules"]) != `["scenario"]` || result["kind"] != "message" || len(array(result["material_actions"])) != 1 {
		t.Fatalf("wrong job result: %s", dump(job))
	}
	if dump(get(app.store.db, "sites", str(site["id"]))) != beforeSite || generationModuleSnapshot("site", object(result["site"])) != generationModuleSnapshot("site", site) {
		t.Fatal("scenario request changed site")
	}
	saved := get(app.store.db, "scenarios", str(scenario["id"]))
	if integer(saved["version"]) != integer(scenario["version"])+1 || object(array(saved["rules"])[0])["path"] != "/requested-change" || len(listing(app.store.db, "scenarios")) != 1 {
		t.Fatal("exact saved scenario not updated")
	}
	for i, value := range array(saved["rules"]) {
		if i > 0 && dump(value) != dump(array(scenario["rules"])[i]) {
			t.Fatal("unrequested rule changed")
		}
	}
	history := app.generation.conversation(str(job["conversation_id"]))
	if dump(object(array(history["turns"])[0])["edit_scope"]) != dump(job["edit_scope"]) {
		t.Fatal("scope missing from conversation history")
	}
	// Scope is selected for each turn, never inherited from a previous job.
	input := app.generation.agentInput(Doc{"prompt": "现在只修改页面", "parent_job_id": job["id"]})
	if dump(object(input["edit_scope"])["modules"]) != `["site"]` {
		t.Fatal("old reference incorrectly restricted the new explicit request")
	}
	generationRequest(t, app, "POST", "jobs", Doc{"prompt": "修改场景", "edit_scope": "invalid"}, 400)
}
