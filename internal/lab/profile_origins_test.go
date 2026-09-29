package lab

import (
	"path/filepath"
	"testing"
)

func TestProfileRevisionOriginUsesExecutionIdentityAndSurvivesRestore(t *testing.T) {
	a := editingAgent(t)
	a.agentConfig = Doc{"id": "writer", "name": "内容编辑助手", "version": 7, "tools": []any{"read_material", "save_material"}}
	a.workspaceID = "workspace-one"
	s := a.manager.store
	profile := s.save("profiles", Doc{"name": "Origin test", "category": "unexpected_output", "body": "INITIAL", "revision_origin": Doc{"kind": "agent", "agent_name": "Forged"}})
	if dump(profile["revision_origin"]) != `{"kind":"manual"}` {
		t.Fatal("manual save accepted client-provided attribution")
	}
	id := str(profile["id"])
	agentOperation(t, a, "read_material", Doc{"kind": "profile", "id": id})
	agentOperation(t, a, "save_material", Doc{"kind": "profile", "id": id, "version": 1, "json": `{"body":"AGENT_EDIT","revision_origin":{"kind":"manual","agent_name":"Forged"}}`})
	agentVersion := s.versions(id)[0]
	origin := object(agentVersion["revision_origin"])
	if origin["kind"] != "agent" || origin["agent_id"] != "writer" || origin["agent_name"] != "内容编辑助手" || integer(origin["agent_version"]) != 7 || origin["job_id"] != a.jobID || origin["workspace_id"] != a.workspaceID {
		t.Fatalf("Agent execution attribution lost: %s", dump(origin))
	}
	// Later changes to the Agent or a manual editor must not rewrite authorship.
	a.agentConfig["name"] = "Renamed later"
	p := clone(agentVersion)
	p["body"] = "MANUAL_EDIT"
	s.save("profiles", p)
	restore := clone(agentVersion)
	restore["version"] = 3
	s.save("profiles", restore)
	versions := s.versions(id)
	if len(versions) != 4 || dump(versions[2]) != dump(agentVersion) || versions[0]["body"] != "AGENT_EDIT" {
		t.Fatal("restoring changed the original Agent revision")
	}
	for _, v := range []Doc{versions[0], versions[1], versions[3]} {
		if dump(v["revision_origin"]) != `{"kind":"manual"}` {
			t.Fatal("manual edit or restore inherited Agent authorship")
		}
	}
}

func TestLegacyProfileOriginsRequireExactSuccessfulSaveEvidence(t *testing.T) {
	a := editingAgent(t)
	a.agentConfig = Doc{"id": "writer", "name": "历史编辑助手", "version": 2, "tools": []any{"read_material", "save_material"}}
	job := a.manager.job(a.jobID)
	job["agent"], job["workspace_id"] = clone(a.agentConfig), "legacy-workspace"
	a.manager.saveJob(job)
	s := a.manager.store
	p := s.save("profiles", Doc{"name": "Legacy origin", "body": "FIRST"})
	id := str(p["id"])
	agentOperation(t, a, "read_material", Doc{"kind": "profile", "id": id})
	agentOperation(t, a, "save_material", Doc{"kind": "profile", "id": id, "version": 1, "json": `{"body":"SECOND"}`})
	p = get(s.db, "profiles", id)
	p["body"] = "THIRD"
	s.save("profiles", p)
	exec(s.db, `UPDATE versions SET doc=json_remove(doc,'$.revision_origin') WHERE profile_id=?`, id)
	// Failed/proposed edits and a successful save of another profile are not proof.
	a.manager.agentEvent(a.jobID, "tool_error", "save failed", Doc{"material": Doc{"kind": "profile", "id": id, "version": 3}})
	a.manager.agentEvent(a.jobID, "material_saved", "unrelated", Doc{"material": Doc{"kind": "profile", "id": "another-profile", "version": 3}})
	p = get(s.db, "profiles", id)
	s.save("profiles", p) // Explicit manual provenance always wins over old audit data.
	a.manager.agentEvent(a.jobID, "material_saved", "contradictory", Doc{"material": Doc{"kind": "profile", "id": id, "version": 4}})
	before := dump(rows(s.db, "SELECT doc FROM versions WHERE profile_id=? ORDER BY version", id))
	versions := s.versions(id)
	origin := object(versions[2]["revision_origin"])
	if origin["kind"] != "agent" || origin["agent_name"] != "历史编辑助手" || origin["job_id"] != a.jobID || origin["workspace_id"] != "legacy-workspace" {
		t.Fatalf("exact historical save not attributed: %s", dump(origin))
	}
	if versions[1]["revision_origin"] != nil || versions[3]["revision_origin"] != nil || object(versions[0]["revision_origin"])["kind"] != "manual" {
		t.Fatal("inferred an author without evidence or overrode explicit provenance")
	}
	if before != dump(rows(s.db, "SELECT doc FROM versions WHERE profile_id=? ORDER BY version", id)) {
		t.Fatal("history read rewrote persisted snapshots")
	}
}

func TestLegacyProfileOriginsWithoutGenerationTable(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "history.sqlite3"), "http://localhost:8765")
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	p := s.save("profiles", Doc{"name": "Legacy", "body": "UNCHANGED"})
	exec(s.db, `UPDATE versions SET doc=json_remove(doc,'$.revision_origin') WHERE profile_id=?`, p["id"])
	if s.versions(str(p["id"]))[0]["revision_origin"] != nil {
		t.Fatal("guessed an author for unattributed history")
	}
}
