package lab

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentQuestionPersistsAndResumesExactlyOnce(t *testing.T) {
	a := generationTestApp(t)
	var calls atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		body := dump(agentRequestBody(r))
		switch calls.Add(1) {
		case 1:
			agentReply(w, agentToolCall("write", "write_file", Doc{"path": "style.css", "content": "/* KEEP PARTIAL FILE */"}),
				agentToolCall("ask", "ask_user", Doc{"question": "首页标题使用什么名称？", "options": []any{"运维中心", "文件中心"}}),
				agentToolCall("bad", "write_file", Doc{"path": "must-not-execute.txt", "content": "BAD"}))
		case 2:
			for _, text := range []string{"创建含样式的站点", "首页标题使用什么名称", "我的运维中心", "style.css", "user_answer"} {
				if !strings.Contains(body, text) {
					t.Errorf("lost question context: %s", text)
				}
			}
			agentReply(w, agentToolCall("read", "read_file", Doc{"path": "style.css"}))
		case 3:
			if !strings.Contains(body, "KEEP PARTIAL FILE") {
				t.Error("lost checkpoint")
			}
			agentReply(w, agentToolCall("write", "write_file", Doc{"path": "index.html", "content": "<h1>我的运维中心</h1>"}), agentToolCall("finish", "finish_draft", Doc{"summary": "已完成首页"}))
		default:
			t.Error("duplicate model execution")
			generationEnvelope(w, "停止")
		}
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "创建含样式的站点", "edit_scope": "site"}, 202)
	id := str(job["id"])
	a.generation.wg.Wait()
	waiting := generationRequest(t, a, "GET", "jobs/"+id, nil, 200)
	if waiting["status"] != "waiting_user" || waiting["checkpoint"] != nil || len(a.generation.active) != 0 {
		t.Fatal("question did not suspend safely", dump(waiting))
	}
	if files := array(object(object(a.generation.job(id)["checkpoint"])["site"])["files"]); len(files) != 1 || object(files[0])["path"] != "style.css" {
		t.Fatal("post-question tool executed or checkpoint lost")
	}
	q := object(waiting["question"])
	generationRequest(t, a, "POST", "jobs", Doc{"prompt": "继续", "conversation_id": id}, 409)
	generationRequest(t, a, "POST", "jobs/"+id+"/answer", Doc{"question_id": "stale", "answer": "hello"}, 409)
	generationRequest(t, a, "POST", "jobs/"+id+"/answer", Doc{"question_id": q["id"], "answer": "  "}, 400)
	generationRequest(t, a, "POST", "jobs", Doc{"prompt": "bad", "answer_to": id, "question_id": q["id"], "agent_id": "writer", "parent_job_id": id, "conversation_id": "unrelated"}, 409)
	// Waiting remains durable without keeping an execution slot or timer alive.
	a.generation.close()
	restarted := &App{store: a.store}
	restarted.generationManager()
	t.Cleanup(restarted.closeGeneration)
	a = restarted
	if a.generation.job(id)["status"] != "waiting_user" {
		t.Fatal("restart erased waiting question")
	}
	answer := Doc{"question_id": q["id"], "answer": "我的运维中心"}
	next := generationRequest(t, a, "POST", "jobs/"+id+"/answer", answer, 202)
	state := object(next["resume_state"])
	if state["reason"] != "user_answer" || !boolean(state["draft_restored"]) || !strings.Contains(str(state["detail"]), "已收到回答") || strings.Contains(str(state["detail"]), "中断") {
		t.Fatal("normal answer was labelled as an interrupted task", dump(state))
	}
	again := generationRequest(t, a, "POST", "jobs/"+id+"/answer", answer, 202)
	if next["id"] != again["id"] {
		t.Fatal("duplicate answer forked task")
	}
	done := generationWait(t, a, str(next["id"]))
	a.generation.wg.Wait()
	if done["status"] != "completed" || done["conversation_id"] != id || len(array(object(object(done["result"])["site"])["files"])) != 2 {
		t.Fatal("resume failed", dump(done))
	}
	if dump(object(done["edit_scope"])["modules"]) != `["site"]` {
		t.Fatal("answer broadened original scope")
	}
	for _, value := range array(done["events"]) {
		event := object(value)
		if event["stage"] == "resumed" && event["detail"] != state["detail"] {
			t.Fatal("answer event disagrees with continuation state", dump(event))
		}
	}
	generationRequest(t, a, "POST", "jobs/"+id+"/answer", Doc{"question_id": q["id"], "answer": "different"}, 409)
	history := a.generation.conversation(id)
	if len(array(history["turns"])) != 2 || object(object(array(history["turns"])[0])["question"])["answer"] != "我的运维中心" {
		t.Fatal("answer history missing", dump(history))
	}
}

func TestAgentQuestionValidationCancellationAndBindings(t *testing.T) {
	a := generationTestApp(t)
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		agentReply(w, agentToolCall("ask", "ask_user", Doc{"question": "使用哪个站点？"}))
	})
	j := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "请调整站点"}, 202)
	a.generation.wg.Wait()
	id := str(j["id"])
	q := object(a.generation.job(id)["question"])
	generationRequest(t, a, "POST", "jobs/"+id+"/cancel", nil, 200)
	generationRequest(t, a, "POST", "jobs/"+id+"/answer", Doc{"question_id": q["id"], "answer": "root"}, 409)
	agent := editingAgent(t)
	for _, args := range []Doc{{"question": ""}, {"question": "q", "options": []any{"a", "a"}}, {"question": "q", "options": []any{1}}, {"question": "q", "options": []any{"1", "2", "3", "4", "5", "6"}}} {
		if _, err := agent.perform(context.Background(), "ask_user", args); err == nil {
			t.Fatal("invalid question accepted", dump(args))
		}
	}
	agent.agentConfig = Doc{"tools": []any{"read_file"}}
	if _, err := agent.perform(context.Background(), "ask_user", Doc{"question": "q"}); err == nil {
		t.Fatal("unbound question allowed")
	}
}

func TestReviewQuestionContinuesInSameConversation(t *testing.T) {
	b := newLab(t)
	_, _, workspace := composerFixture(b)
	var round atomic.Int32
	agentModel(t, b.app, func(w http.ResponseWriter, r *http.Request) {
		body := dump(agentRequestBody(r))
		if round.Add(1) == 1 {
			agentReply(w, agentToolCall("read", "read_workspace", Doc{}), agentToolCall("ask", "ask_user", Doc{"question": "需要重点检查哪个入口？"}))
			return
		}
		if !strings.Contains(body, "需要重点检查哪个入口") || !strings.Contains(body, "登录入口") {
			t.Error("review lost question/answer")
		}
		generationEnvelope(w, "已记录需要核查登录入口")
	})
	job := generationRequest(t, b.app, "POST", "jobs", Doc{"workspace_id": workspace["id"], "agent_id": "reviewer", "prompt": "帮我核查"}, 202)
	b.app.generation.wg.Wait()
	waiting := b.app.generation.job(str(job["id"]))
	if waiting["status"] != "waiting_user" || object(object(waiting["result"])["review_progress"]) == nil {
		t.Fatal("review did not suspend", dump(waiting))
	}
	next := generationRequest(t, b.app, "POST", "jobs/"+str(job["id"])+"/answer", Doc{"question_id": object(waiting["question"])["id"], "answer": "登录入口"}, 202)
	done := generationWait(t, b.app, str(next["id"]))
	if done["status"] != "completed" || done["conversation_id"] != job["conversation_id"] || object(done["agent"])["id"] != "reviewer" {
		t.Fatal("review answer switched conversation or role")
	}
}
