package lab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentRoundSettingsAllowOneHundredAndRejectInvalid(t *testing.T) {
	b := newLab(t)
	for _, role := range []string{"writer", "reviewer", "redteam"} {
		config := get(b.app.store.db, "agent_definitions", role)
		for _, rounds := range []int{1, 48, 100} {
			config["max_rounds"] = rounds
			config = b.api("/agents", "POST", config, 200)
			if integer(config["max_rounds"]) != rounds {
				t.Fatal("configured round limit was changed")
			}
		}
		for _, invalid := range []any{0, 101, json.Number("1.5"), "100", nil} {
			bad := clone(config)
			bad["max_rounds"] = invalid
			reply := b.api("/agents", "POST", bad, 400)
			if !strings.Contains(str(reply["error"]), "1–100") {
				t.Fatal("incorrect range feedback", reply)
			}
		}
		if dump(get(b.app.store.db, "agent_definitions", role)) != dump(config) {
			t.Fatal("invalid save mutated config")
		}
	}
	b.restart()
	for _, role := range []string{"writer", "reviewer", "redteam"} {
		if integer(get(b.app.store.db, "agent_definitions", role)["max_rounds"]) != 100 {
			t.Fatal("restart reset round limit")
		}
	}
}

func TestAgentRunsOneHundredRoundsWithConfigSnapshot(t *testing.T) {
	for _, role := range []string{"writer", "reviewer", "redteam"} {
		t.Run(role, func(t *testing.T) {
			b := newLab(t)
			_, _, workspace := composerFixture(b)
			config := get(b.app.store.db, "agent_definitions", role)
			config["max_rounds"] = 100
			b.api("/agents", "POST", config, 200)
			var round atomic.Int32
			agentModel(t, b.app, func(w http.ResponseWriter, r *http.Request) {
				// Consume the complete request, as a provider would, without retaining it.
				agentRequestBody(r)
				n := round.Add(1)
				if n == 1 {
					// An edit during a run applies only to future jobs, never this snapshot.
					updated := get(b.app.store.db, "agent_definitions", role)
					updated["max_rounds"] = 2
					b.app.store.saveAgentDefinition(updated)
				}
				if n == 100 {
					generationEnvelope(w, "完成第 100 轮")
					return
				}
				tool, args := "list_files", Doc{}
				if role == "reviewer" {
					tool = "read_workspace"
				}
				if role == "redteam" {
					tool, args = "redteam_request", Doc{"path": "/"}
				}
				agentReply(w, agentToolCall(fmt.Sprintf("round-%d", n), tool, args))
			})
			job := generationRequest(t, b.app, "POST", "jobs", Doc{"workspace_id": workspace["id"], "agent_id": role, "prompt": "按指定步骤检查当前工作区"}, 202)
			b.app.generation.wg.Wait()
			done := b.app.generation.job(str(job["id"]))
			if done["status"] != "completed" || round.Load() != 100 {
				t.Fatalf("%s stopped at round %d: %v", role, round.Load(), done["error"])
			}
			if integer(object(done["agent"])["max_rounds"]) != 100 || integer(get(b.app.store.db, "agent_definitions", role)["max_rounds"]) != 2 {
				t.Fatal("running config was not isolated from later saves")
			}
		})
	}
}
