package lab

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentRegistryBindingsAreEnforcedAndVersioned(t *testing.T) {
	b := newLab(t)
	list := b.api("/agents", "GET", nil, 200)
	if len(array(list["items"])) != 3 {
		t.Fatal("missing built-in roles")
	}
	d := get(b.app.store.db, "agent_definitions", "writer")
	d["tools"] = []any{"read_file", "finish_draft"}
	saved := b.api("/agents", "POST", d, 200)
	b.api("/agents", "POST", d, 409)
	a := editingAgent(t)
	a.agentConfig = saved
	a.site = siteFixture()
	before := dump(a.site)
	if _, err := a.perform(context.Background(), "write_file", Doc{"path": "index.html", "content": "wrong"}); err == nil {
		t.Fatal("unbound tool executed")
	}
	if dump(a.site) != before {
		t.Fatal("rejected tool changed file")
	}
	if len(a.scopedTools()) != 2 {
		t.Fatal("provider schemas ignore binding")
	}
	invalid := clone(saved)
	invalid["tools"] = []any{"finish_draft", "review_request"}
	b.api("/agents", "POST", invalid, 400)
	custom := clone(saved)
	delete(custom, "id")
	custom["name"] = "Specialist"
	created := b.api("/agents", "POST", custom, 200)
	if created["id"] == "writer" || boolean(created["builtin"]) {
		t.Fatal("custom agent replaced built-in")
	}
	b.restart()
	if dump(get(b.app.store.db, "agent_definitions", "writer")) != dump(saved) {
		t.Fatal("restart reset bindings")
	}
}

func TestWorkspaceReviewChecksRuntimeWithoutProductionWrites(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	before := dump(listing(b.app.store.db, "workspaces"))
	events := count(b.app.store.db, "SELECT COUNT(*) FROM events")
	job := b.api("/workspace-reviews/"+str(workspace["id"]), "POST", Doc{}, 202)
	done := generationWait(t, b.app, str(job["id"]))
	if done["status"] != "completed" {
		t.Fatalf("%s", dump(done))
	}
	report := object(done["result"])
	if integer(object(report["counts"])["fail"]) != 0 {
		t.Fatalf("unexpected failure %s", dump(report))
	}
	if report["verdict"] != "incomplete" {
		t.Fatal("without model/browser must not claim whole flow passes")
	}
	found := map[string]bool{}
	for _, raw := range array(report["checks"]) {
		c := object(raw)
		found[str(c["name"])] = true
		if strings.Contains(str(c["name"]), "Conditional response · GET") && c["status"] != "pass" {
			t.Fatalf("route did not pass: %s", dump(c))
		}
	}
	if !found["回传 · 有效令牌"] || !found["回传 · 旧令牌失效"] {
		t.Fatal("missing callback checks")
	}
	if dump(listing(b.app.store.db, "workspaces")) != before || count(b.app.store.db, "SELECT COUNT(*) FROM events") != events || count(b.app.store.db, "SELECT COUNT(*) FROM sessions") != 0 || count(b.app.store.db, "SELECT COUNT(*) FROM reports") != 0 {
		t.Fatal("review mutated production")
	}
	if len(b.app.generation.listConversations(str(workspace["id"]))) != 1 {
		t.Fatal("review missing from shared assistant conversations")
	}
}

func TestReviewFindsShadowedRulesAndDoesNotFollowExternalRequests(t *testing.T) {
	b := newLab(t)
	_, scenario, workspace := composerFixture(b)
	rule := clone(object(array(scenario["rules"])[0]))
	rule["id"] = "catch-all"
	rule["conditions"] = []any{}
	scenario["rules"] = append([]any{rule}, array(scenario["rules"])...)
	b.api("/scenarios", "POST", scenario, 200)
	job := b.api("/workspace-reviews/"+str(workspace["id"]), "POST", Doc{}, 202)
	done := generationWait(t, b.app, str(job["id"]))
	if integer(object(object(done["result"])["counts"])["fail"]) < 1 {
		t.Fatal("unreachable rule not flagged")
	}
	r := &workspaceReviewer{manager: b.app.generation, jobID: str(job["id"]), snapshot: reviewSnapshot(b.app.store.db, str(workspace["id"])), config: get(b.app.store.db, "agent_definitions", "reviewer")}
	if err := r.open(); err != nil {
		t.Fatal(err)
	}
	defer r.sandbox.close()
	for _, path := range []string{"https://example.com/", "//127.0.0.1:8766/api/state"} {
		if _, err := r.call(context.Background(), "review_request", Doc{"path": path}); err == nil {
			t.Fatal("external path accepted")
		}
	}
	if _, err := r.call(context.Background(), "write_file", Doc{"path": "index.html", "content": "bad"}); err == nil {
		t.Fatal("reviewer acquired writer tools")
	}
}

func TestReviewTriggersPersistCoalesceAndFollowLatestMaterial(t *testing.T) {
	b := newLab(t)
	site, _, workspace := composerFixture(b)
	id := str(workspace["id"])
	policy := b.api("/workspace-reviews/"+id+"/policy", "POST", Doc{"version": 0, "agent_id": "reviewer", "after_save": true, "after_writer": false, "browser_steps": []any{}}, 200)
	b.app.generation.pollReviewTriggers()
	if count(b.app.store.db, "SELECT COUNT(*) FROM generation_jobs") != 0 {
		t.Fatal("enabling replayed old events")
	}
	object(array(site["files"])[0])["content"] = "<h1>Changed</h1>"
	site = b.api("/sites", "POST", site, 200)
	b.app.generation.pollReviewTriggers()
	jobs := array(b.api("/workspace-reviews/"+id, "GET", nil, 200)["items"])
	if len(jobs) != 1 {
		t.Fatal("save did not trigger review")
	}
	generationWait(t, b.app, str(object(jobs[0])["id"]))
	for i := 0; i < 4; i++ {
		b.app.generation.pollReviewTriggers()
	}
	if count(b.app.store.db, "SELECT COUNT(*) FROM generation_jobs") != 1 {
		t.Fatal("duplicate unchanged review")
	}
	b.restart()
	b.app.generation.pollReviewTriggers()
	if count(b.app.store.db, "SELECT COUNT(*) FROM generation_jobs") != 1 {
		t.Fatal("restart replayed old event")
	}
	policy["after_save"], policy["after_writer"] = false, true
	b.api("/workspace-reviews/"+id+"/policy", "POST", policy, 200)
	// Completed writer events trigger even when they did not mutate a material.
	b.app.generation.saveJob(Doc{"id": "writer-event", "mode": "agent", "workspace_id": id, "status": "completed", "updated_at": time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)})
	b.app.generation.pollReviewTriggers()
	jobs = array(b.api("/workspace-reviews/"+id, "GET", nil, 200)["items"])
	if len(jobs) != 2 || object(jobs[0])["trigger"] != "after_writer" {
		t.Fatal("writer completion not dispatched")
	}
	generationWait(t, b.app, str(object(jobs[0])["id"]))
}

func TestReviewAgentCallsBoundToolsAndKeepsEvidenceAfterProviderError(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	var calls atomic.Int32
	agentModel(t, b.app, func(w http.ResponseWriter, req *http.Request) {
		body := agentRequestBody(req)
		for _, v := range array(body["tools"]) {
			if str(object(object(v)["function"])["name"]) == "write_file" {
				t.Error("review offered writer tool")
			}
		}
		switch calls.Add(1) {
		case 1:
			agentReply(w, agentToolCall("inspect", "read_workspace", Doc{}))
		case 2:
			agentReply(w, agentToolCall("incorrect", "write_file", Doc{"path": "index.html", "content": "bad"}))
		default:
			w.WriteHeader(400)
		}
	})
	job := b.api("/workspace-reviews/"+str(workspace["id"]), "POST", Doc{}, 202)
	done := generationWait(t, b.app, str(job["id"]))
	if done["status"] != "completed" {
		t.Fatalf("partial report discarded: %s", dump(done))
	}
	if integer(object(object(done["result"])["counts"])["incomplete"]) < 2 {
		t.Fatal("missing failure or coverage explanation")
	}
}

func TestReviewCancellationKeepsPartialReportAndConfigSnapshot(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	started := make(chan struct{}, 2)
	stop := make(chan struct{})
	agentModel(t, b.app, func(w http.ResponseWriter, req *http.Request) {
		_ = agentRequestBody(req)
		started <- struct{}{}
		select {
		case <-req.Context().Done():
		case <-stop:
		}
	})
	defer close(stop)
	job := b.api("/workspace-reviews/"+str(workspace["id"]), "POST", Doc{}, 202)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("review did not reach model")
	}
	config := get(b.app.store.db, "agent_definitions", "reviewer")
	old := dump(config)
	config["tools"] = []any{"finish_review"}
	b.api("/agents", "POST", config, 200)
	b.api("/generation/jobs/"+str(job["id"])+"/cancel", "POST", Doc{}, 200)
	b.app.generation.wg.Wait()
	done := b.app.generation.job(str(job["id"]))
	if done["status"] != "cancelled" || len(array(object(done["result"])["checks"])) == 0 {
		t.Fatalf("cancel discarded evidence: %s", dump(done))
	}
	if dump(object(done["agent"])) != old {
		t.Fatal("running configuration was mutated")
	}
	if integer(object(object(done["result"])["counts"])["incomplete"]) == 0 {
		t.Fatal("interrupted review claimed complete coverage")
	}
}

func TestReviewTriggerFailureIsVisibleAndWorkspaceDeleteStopsTrigger(t *testing.T) {
	b := newLab(t)
	site, _, workspace := composerFixture(b)
	id := str(workspace["id"])
	b.api("/workspace-reviews/"+id+"/policy", "POST", Doc{"version": 0, "agent_id": "reviewer", "after_save": true, "after_writer": false}, 200)
	config := get(b.app.store.db, "agent_definitions", "reviewer")
	config["enabled"] = false
	b.api("/agents", "POST", config, 200)
	object(array(site["files"])[0])["content"] = "<h1>new</h1>"
	b.api("/sites", "POST", site, 200)
	b.app.generation.pollReviewTriggers()
	policy := b.api("/workspace-reviews/"+id+"/policy", "GET", nil, 200)
	if !strings.Contains(str(policy["trigger_error"]), "停用") {
		t.Fatal("trigger failure silently swallowed")
	}
	b.app.listeners.deleteWorkspace(id)
	if entityMaybe(b.app.store.db, "review_policies", id) != nil || entityMaybe(b.app.store.db, "review_cursors", id) != nil {
		t.Fatal("deleted workspace kept automatic subscription")
	}
}

func TestReviewerSharesChatAndConsultationDoesNotRunSuite(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	var requests atomic.Int32
	agentModel(t, b.app, func(w http.ResponseWriter, r *http.Request) {
		body := agentRequestBody(r)
		if requests.Add(1) == 2 && !strings.Contains(dump(body["messages"]), "CAPABILITY_REPLY") {
			t.Error("follow-up lost previous reply")
		}
		for _, tool := range array(body["tools"]) {
			if object(object(tool)["function"])["name"] == "write_file" {
				t.Error("reviewer received writer tool")
			}
		}
		generationEnvelope(w, "CAPABILITY_REPLY: 可以核查主页、登录及接口链路。")
	})
	raw := Doc{"workspace_id": workspace["id"], "agent_id": "reviewer", "prompt": "你可以做什么？"}
	first := b.api("/generation/jobs", "POST", raw, 202)
	done := generationWait(t, b.app, str(first["id"]))
	if done["status"] != "completed" || object(done["result"])["kind"] != "message" {
		t.Fatal("consultation became a report", dump(done))
	}
	for _, e := range array(done["events"]) {
		if object(e)["stage"] == "tool_call" {
			t.Fatal("consultation ran automatic suite")
		}
	}
	list := b.app.generation.listConversations(str(workspace["id"]))
	if len(list) != 1 || object(object(list[0])["agent"])["role"] != "reviewer" {
		t.Fatal("reviewer missing in chat")
	}
	raw["conversation_id"], raw["prompt"] = first["conversation_id"], "请解释刚才说的登录链路"
	second := b.api("/generation/jobs", "POST", raw, 202)
	generationWait(t, b.app, str(second["id"]))
	history := b.app.generation.conversation(str(first["conversation_id"]))
	if integer(history["total_turns"]) != 2 {
		t.Fatal("follow-up split conversation")
	}
	raw["agent_id"] = "writer"
	b.api("/generation/jobs", "POST", raw, 409)
	b.restart()
	if len(b.app.generation.listConversations(str(workspace["id"]))) != 1 {
		t.Fatal("restart lost shared chat")
	}
}

func TestReviewAdvancedTriggersUseChatAndIgnoreReviewerEvents(t *testing.T) {
	for _, trigger := range []string{"on_tool", "after_failure", "interval"} {
		t.Run(trigger, func(t *testing.T) {
			b := newLab(t)
			_, _, workspace := composerFixture(b)
			id := str(workspace["id"])
			raw := Doc{"version": 0, "agent_id": "reviewer", "browser_steps": []any{}, "messages": Doc{trigger: "CUSTOM_REVIEW_MESSAGE"}}
			if trigger == "interval" {
				raw["interval_seconds"] = 30
			} else {
				raw[trigger] = true
			}
			if trigger == "on_tool" {
				raw["tool_names"] = []any{"save_material"}
			}
			b.api("/workspace-reviews/"+id+"/policy", "POST", raw, 200)
			b.app.generation.pollReviewTriggers()
			if count(b.app.store.db, "SELECT COUNT(*) FROM generation_jobs") != 0 {
				t.Fatal("enable replayed old events")
			}
			stamp := time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
			if trigger == "interval" {
				b.app.store.write(func(q queryer) {
					c := get(q, "review_cursors", id)
					c["last_run"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
					put(q, "review_cursors", c)
				})
			} else {
				b.app.generation.saveJob(Doc{"id": "writertrigger", "mode": "agent", "workspace_id": id, "status": "failed", "updated_at": stamp, "events": []any{Doc{"stage": "tool_result", "tool": "save_material", "time": stamp}}})
			}
			b.app.generation.pollReviewTriggers()
			jobs := array(b.api("/workspace-reviews/"+id, "GET", nil, 200)["items"])
			if len(jobs) != 1 || object(jobs[0])["trigger"] != trigger || object(jobs[0])["prompt"] != "CUSTOM_REVIEW_MESSAGE" {
				t.Fatal("trigger not dispatched", dump(jobs))
			}
			done := generationWait(t, b.app, str(object(jobs[0])["id"]))
			b.app.generation.pollReviewTriggers()
			if len(array(b.api("/workspace-reviews/"+id, "GET", nil, 200)["items"])) != 1 {
				t.Fatal("review event caused recursion")
			}
			if str(done["conversation_id"]) == "" {
				t.Fatal("auto review missing follow-up conversation")
			}
		})
	}
}
