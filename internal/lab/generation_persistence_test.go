package lab

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPersistenceStateRecognizesSavedCopiesWithoutGrantingWriteAuthority(t *testing.T) {
	b := newLab(t)
	site, scenario, workspace := composerFixture(b)
	a := &draftAgent{manager: b.app.generation, workspaceID: str(workspace["id"]), site: clone(site), scenario: clone(scenario)}
	delete(a.site, "id")
	delete(a.scenario, "id")
	a.initialSite = generationModuleSnapshot("site", a.site)
	a.initialScenario = generationModuleSnapshot("scenario", a.scenario)
	before := dump(listing(b.app.store.db, "sites"))
	state := a.persistenceState()
	if !boolean(object(state["workspace"])["requires_first_publish"]) {
		t.Fatal("unpublished workspace hidden")
	}
	for _, kind := range []string{"site", "scenario"} {
		d := object(object(state["drafts"])[kind])
		if d["state"] != "saved" || boolean(d["requires_save"]) || str(d["saved_id"]) == "" {
			t.Fatalf("lost saved identity: %s", dump(d))
		}
	}
	if a.site["id"] != nil || a.scenario["id"] != nil || len(a.materialSnapshots) != 0 || dump(listing(b.app.store.db, "sites")) != before {
		t.Fatal("status lookup mutated state or granted read-before-write authority")
	}
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	listener := b.listener(dep)
	state = a.persistenceState()
	w := object(state["workspace"])
	if !boolean(w["published"]) || boolean(w["requires_first_publish"]) || integer(w["active_ports"]) != 1 {
		t.Fatal("publication state is incorrect")
	}
	listener["enabled"] = false
	b.api("/listeners", "POST", listener, 200)
	if integer(object(a.persistenceState()["workspace"])["active_ports"]) != 0 {
		t.Fatal("disabled listener reported as active")
	}
	file := object(array(a.site["files"])[0])
	file["content"] = str(file["content"]) + "<p>Unsaved</p>"
	if object(object(a.persistenceState()["drafts"])["site"])["state"] != "unsaved_changes" {
		t.Fatal("modified draft misreported as saved")
	}
	a.site = Doc{"name": "Incomplete", "entry": "index.html", "files": []any{}}
	if object(object(a.persistenceState()["drafts"])["site"])["state"] != "unsaved_changes" {
		t.Fatal("partial draft lost")
	}
}

func TestAgentReceivesVerifiedSaveAndHotUpdateState(t *testing.T) {
	b := newLab(t)
	site, scenario, workspace := composerFixture(b)
	dep := b.api("/workspaces/"+str(workspace["id"])+"/publish", "POST", Doc{"version": workspace["version"]}, 200)
	listener := b.listener(dep)
	delete(site, "id") // Same shape as a previous result / continuation snapshot.
	var rounds atomic.Int32
	agentModel(t, b.app, func(w http.ResponseWriter, req *http.Request) {
		body := agentRequestBody(req)
		messages := array(body["messages"])
		switch rounds.Add(1) {
		case 1:
			context := decode(str(object(messages[len(messages)-1])["content"]))
			state := object(context["persistence"])
			if object(object(state["drafts"])["site"])["state"] != "saved" || boolean(object(state["workspace"])["requires_first_publish"]) {
				t.Error("model missing actual persistence state")
			}
			if !strings.Contains(str(object(messages[0])["content"]), "do not save it again") {
				t.Error("missing concise completion instruction")
			}
			agentReply(w, agentToolCall("load", "load_material", Doc{"kind": "scenario", "id": scenario["id"]}))
		case 2:
			rule := clone(object(array(scenario["rules"])[0]))
			rule["path"] = "/api/updated"
			agentReply(w, agentToolCall("edit", "upsert_rule", Doc{"rule_id": "download", "rule": rule}), agentToolCall("save", "save_material", Doc{"kind": "scenario", "id": scenario["id"], "version": scenario["version"]}))
		case 3:
			output := decode(str(object(messages[len(messages)-1])["content"]))
			state := object(output["persistence"])
			if object(object(state["drafts"])["scenario"])["state"] != "saved" || object(object(state["drafts"])["site"])["state"] != "saved" {
				t.Error("save tool did not confirm actual saved modules")
			}
			if !strings.Contains(str(output["live_update"]), "无需重复") {
				t.Error("save still suggests manual repeat")
			}
			agentReply(w, agentToolCall("finish", "finish_draft", Doc{"summary": "接口路径已更新并保存，已同步到绑定端口。"}))
		default:
			t.Error("unnecessary additional save/model turn")
		}
	})
	job := generationRequest(t, b.app, "POST", "jobs", Doc{"workspace_id": workspace["id"], "site": site, "scenario": scenario, "edit_scope": "scenario", "prompt": "修改场景的下载路径并保存"}, 202)
	done := generationWait(t, b.app, str(job["id"]))
	if done["status"] != "completed" || object(done["result"])["kind"] != "message" {
		t.Fatalf("saved changes still require adoption: %s", dump(done))
	}
	if integer(get(b.app.store.db, "scenarios", str(scenario["id"]))["version"]) != 2 {
		t.Fatal("duplicate save/version")
	}
	response := b.request(str(listener["public_url"]), "/api/updated?file=notes.txt", "GET", nil)
	if response.status != 200 || !strings.Contains(string(response.body), "CANARY") {
		t.Fatal("save did not update live endpoint")
	}
}
