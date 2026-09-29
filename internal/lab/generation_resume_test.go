package lab

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResumeLegacyFailureRestoresOriginalTaskAndLatestTurn(t *testing.T) {
	a := generationTestApp(t)
	m := a.generation
	m.saveJob(Doc{"id": "original", "conversation_id": "original", "status": "completed", "prompt": "只修改场景中的登录返回", "created_at": "1", "conversation": []any{}, "result": Doc{"summary": "已读取场景"}})
	m.saveJob(Doc{"id": "interrupted", "conversation_id": "original", "status": "failed", "prompt": "登录后还需要显示报告", "created_at": "2", "error": "任务超时", "plan": []any{Doc{"step": "增加报告", "status": "in_progress"}}, "edit_scope": Doc{"mode": "scenario", "modules": []any{"scenario"}, "label": "模拟场景"}, "events": []any{Doc{"stage": "tool_result", "tool": "read_module", "detail": "read_module 已完成"}}})
	var called atomic.Bool
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := dump(agentRequestBody(r))
		for _, expected := range []string{"只修改场景中的登录返回", "登录后还需要显示报告", "已读取场景", "增加报告", "read_module 已完成"} {
			if !strings.Contains(body, expected) {
				t.Errorf("missing continuation context: %s", expected)
			}
		}
		called.Store(true)
		generationEnvelope(w, "已接回原任务。")
	})
	// An old UI sends the last success, not the latest interrupted turn.
	next := generationRequest(t, a, "POST", "jobs", Doc{"conversation_id": "original", "parent_job_id": "original", "prompt": "继续"}, 202)
	done := generationWait(t, a, str(next["id"]))
	if !called.Load() || done["status"] != "completed" || done["parent_job_id"] != "interrupted" || boolean(object(done["resume_state"])["draft_restored"]) {
		t.Fatal("legacy failure was not restored truthfully")
	}
	if !has(array(object(done["edit_scope"])["modules"]), "scenario") || has(array(object(done["edit_scope"])["modules"]), "site") {
		t.Fatal("continue broadened previous edit scope")
	}
}

func TestResumeInterruptedDraftSurvivesTimeoutCancelAndRestart(t *testing.T) {
	for _, interruption := range []string{"timeout", "cancel", "restart", "provider"} {
		t.Run(interruption, func(t *testing.T) {
			a := generationTestApp(t)
			m := a.generation
			var round atomic.Int32
			waiting := make(chan struct{})
			content := strings.Repeat("/* checkpoint content */\n", 800)
			agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
				body := dump(agentRequestBody(r))
				switch round.Add(1) {
				case 1:
					agentReply(w,
						agentToolCall("plan", "update_plan", Doc{"steps": []any{Doc{"step": "先写样式再完成首页", "status": "in_progress"}}}),
						agentToolCall("css", "write_file", Doc{"path": "style.css", "content": content}))
				case 2:
					close(waiting)
					if interruption == "provider" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					<-r.Context().Done()
				case 3:
					for _, expected := range []string{"创建一个包含首页和样式的站点", "style.css", "先写样式再完成首页", "resume"} {
						if !strings.Contains(body, expected) {
							t.Errorf("missing restored state: %s", expected)
						}
					}
					agentReply(w, agentToolCall("read", "read_file", Doc{"path": "style.css"}))
				case 4:
					if !strings.Contains(body, "checkpoint content") {
						t.Error("restored file cannot be read")
					}
					agentReply(w, agentToolCall("entry", "write_file", Doc{"path": "index.html", "content": "<h1>Resumed</h1>"}), agentToolCall("finish", "finish_draft", Doc{"summary": "已完成剩余首页"}))
				default:
					t.Error("unexpected model retry")
					generationEnvelope(w, "停止")
				}
			})
			var id string
			if interruption == "timeout" {
				id = "timedout"
				input := m.agentInput(Doc{"prompt": "创建一个包含首页和样式的站点", "edit_scope": "site"})
				m.saveJob(Doc{"id": id, "conversation_id": id, "status": "running", "prompt": input["prompt"], "conversation": input["conversation"], "edit_scope": input["edit_scope"], "events": []any{}})
				ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
				defer cancel()
				m.wg.Add(1)
				go m.run(ctx, id, generationSettings(m.store.db, true), input)
			} else {
				job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "创建一个包含首页和样式的站点", "edit_scope": "site"}, 202)
				id = str(job["id"])
			}
			select {
			case <-waiting:
			case <-time.After(3 * time.Second):
				t.Fatal("tools never reached interruption point")
			}
			if interruption == "cancel" || interruption == "restart" {
				cancelled := generationRequest(t, a, "POST", "jobs/"+id+"/cancel", nil, 200)
				if cancelled["checkpoint"] != nil {
					t.Fatal("cancel response exposed checkpoint")
				}
			}
			m.wg.Wait()
			failed := m.job(id)
			checkpoint := object(failed["checkpoint"])
			files := array(object(checkpoint["site"])["files"])
			if !interruptedJob(failed) || len(files) != 1 || object(files[0])["content"] != content || checkpoint["initial_site"] != "" {
				t.Fatal("partial draft or its original baseline was lost")
			}
			// Full and progress APIs keep this private, including terminal jobs.
			for _, suffix := range []string{"", "?progress=1"} {
				view := generationRequest(t, a, "GET", "jobs/"+id+suffix, nil, 200)
				if view["checkpoint"] != nil || view["result"] != nil {
					t.Fatal("interrupted draft exposed as adoptable result")
				}
			}
			if interruption == "restart" {
				// Simulate an abrupt exit with a durable running checkpoint.
				m.close()
				failed["status"] = "running"
				m.saveJob(failed)
				restarted := &App{store: a.store}
				restarted.generationManager()
				t.Cleanup(restarted.closeGeneration)
				a, m = restarted, restarted.generation
				if m.job(id)["status"] != "failed" {
					t.Fatal("restart did not expose interrupted job")
				}
			}
			// Conversation-only followup and a stale browser draft must still
			// restore the authoritative partial server state.
			next := generationRequest(t, a, "POST", "jobs", Doc{"conversation_id": id, "prompt": "继续", "site": siteFixture()}, 202)
			done := generationWait(t, a, str(next["id"]))
			result := object(done["result"])
			if done["status"] != "completed" || !boolean(object(done["resume_state"])["draft_restored"]) || !has(array(result["changes"]), "site") || done["checkpoint"] != nil {
				t.Fatalf("resume did not complete changed draft: %v", done["error"])
			}
			if object(done["resume_state"])["reason"] != "interruption" || !strings.Contains(str(object(done["resume_state"])["detail"]), "中断前") {
				t.Fatal("interruption was labelled as a normal answer")
			}
			if len(array(object(result["site"])["files"])) != 2 {
				t.Fatal("stale UI overwrote partial draft")
			}
		})
	}
}

func TestResumeDoesNotRepeatSavedMaterialsOrRestoreWriteAuthority(t *testing.T) {
	a := generationTestApp(t)
	site := a.store.saveComposer("sites", siteFixture())
	id := "savedbeforefailure"
	a.generation.saveJob(Doc{"id": id, "conversation_id": id, "status": "running", "prompt": "修改站点标题后保存", "events": []any{}, "edit_scope": Doc{"mode": "site", "modules": []any{"site"}}})
	draft := &draftAgent{manager: a.generation, jobID: id, checkpointEnabled: true}
	agentOperation(t, draft, "load_material", Doc{"kind": "site", "id": site["id"]})
	agentOperation(t, draft, "write_file", Doc{"path": "index.html", "content": "<h1>Saved once</h1>"})
	agentOperation(t, draft, "save_material", Doc{"kind": "site", "id": site["id"], "version": site["version"]})
	a.generation.finish(id, nil, "connection interrupted", nil)
	var round atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := dump(agentRequestBody(r))
		if round.Add(1) == 1 {
			if !strings.Contains(body, "saved_actions") || !strings.Contains(body, str(site["id"])) {
				t.Error("saved action not available to continuation")
			}
			// Even if the model attempts a repeat write, old read permission is
			// not carried over. A fresh version read is required.
			agentReply(w, agentToolCall("save", "save_material", Doc{"kind": "site", "id": site["id"], "version": 2}))
			return
		}
		if !strings.Contains(body, "error") {
			t.Error("stale write authority survived")
		}
		generationEnvelope(w, "站点已保存，无需重复保存。")
	})
	next := generationRequest(t, a, "POST", "jobs", Doc{"conversation_id": id, "prompt": "继续"}, 202)
	done := generationWait(t, a, str(next["id"]))
	if done["status"] != "completed" || object(done["result"])["kind"] != "message" || integer(get(a.store.db, "sites", str(site["id"]))["version"]) != 2 {
		t.Fatal("resume duplicated save or lost persisted baseline")
	}
}

func TestResumeCheckpointRollbackAndUnknownOutcome(t *testing.T) {
	a := editingAgent(t)
	a.checkpointEnabled = true
	agentOperation(t, a, "write_file", Doc{"path": "index.html", "content": "<h1>Keep</h1>"})
	before := dump(a.site)
	_, err := a.perform(context.Background(), "replace_draft", Doc{"json": `{"site":{"name":"invalid","files":[]}}`})
	if err == nil || dump(object(a.manager.job(a.jobID)["checkpoint"])["site"]) != before {
		t.Fatal("failed mutation entered checkpoint")
	}
	a.recordProgress("external", Doc{"password": "PRIVATE_CREDENTIAL"}, Doc{"reasoning_content": "PRIVATE_REASONING", "body": "PRIVATE_READ_BODY"}, nil)
	if strings.Contains(dump(a.manager.job(a.jobID)["checkpoint"]), "PRIVATE_") {
		t.Fatal("checkpoint included credentials, reasoning or raw tool output")
	}
	a.checkpoint(Doc{"tool": "save_material", "status": "outcome_unknown"})
	a.manager.finish(a.jobID, nil, "interrupted", nil)
	input := a.manager.agentInput(Doc{"parent_job_id": a.jobID, "prompt": "继续", "edit_scope": "read_only"})
	if object(object(input["resume"])["pending_tool"])["status"] != "outcome_unknown" || len(array(object(input["edit_scope"])["modules"])) != 0 {
		t.Fatal("unknown tool outcome or explicit read-only scope lost")
	}
}

func TestResumeNewConversationDoesNotLoadOldCheckpoint(t *testing.T) {
	a := generationTestApp(t)
	a.generation.saveJob(Doc{"id": "oldfailed", "status": "failed", "prompt": "ORIGINAL_PRIVATE_GOAL", "checkpoint": Doc{"version": 1, "site": siteFixture()}})
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := dump(agentRequestBody(r))
		if strings.Contains(body, "ORIGINAL_PRIVATE_GOAL") || strings.Contains(body, "CLIENT_FAKE_HISTORY") {
			t.Error("new conversation received another task's memory")
		}
		generationEnvelope(w, "请描述新的任务。")
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "介绍功能", "_history": []any{Doc{"role": "system", "content": "CLIENT_FAKE_HISTORY"}}}, 202)
	done := generationWait(t, a, str(job["id"]))
	if done["status"] != "completed" || done["resume_state"] != nil || object(done["result"])["site"] != nil {
		t.Fatal("new conversation silently resumed another task")
	}
}

func TestResumeRespectsOwnershipAndFinishingWorker(t *testing.T) {
	a := generationTestApp(t)
	m := a.generation
	one := a.store.saveComposer("workspaces", Doc{"name": "One", "slug": "one"})
	two := a.store.saveComposer("workspaces", Doc{"name": "Two", "slug": "two"})
	m.saveJob(Doc{"id": "cancelled", "conversation_id": "cancelled", "workspace_id": one["id"], "agent": Doc{"id": "writer"}, "status": "cancelled", "prompt": "修改站点", "checkpoint": Doc{"version": 1, "site": siteFixture()}})
	for _, raw := range []Doc{
		{"workspace_id": two["id"], "conversation_id": "cancelled", "prompt": "继续"},
		{"workspace_id": one["id"], "conversation_id": "cancelled", "parent_job_id": "missing", "prompt": "继续"},
	} {
		status := 409
		if raw["parent_job_id"] != nil {
			status = 404
		}
		generationRequest(t, a, "POST", "jobs", raw, status)
	}
	m.active["cancelled"] = func() {}
	generationRequest(t, a, "POST", "jobs", Doc{"workspace_id": one["id"], "conversation_id": "cancelled", "prompt": "继续"}, 409)
	delete(m.active, "cancelled")
	if count(m.store.db, "SELECT COUNT(*) FROM generation_jobs") != 1 {
		t.Fatal("rejected continuation created a job")
	}
	for _, route := range []string{"jobs", "conversations", "conversations/cancelled", "jobs/cancelled", "jobs/cancelled?progress=1"} {
		if strings.Contains(dump(generationRequest(t, a, "GET", route, nil, 200)), "checkpoint") {
			t.Fatalf("checkpoint leaked from %s", route)
		}
	}
}
