package lab

// Record the Agent identity at the time of execution, not its mutable current
// configuration or metadata supplied by a model. Prompt text and credentials
// are deliberately excluded from the attribution.
func agentProfileOrigin(agent Doc, jobID, workspaceID string) Doc {
	return Doc{
		"kind": "agent", "agent_id": agent["id"], "agent_name": agent["name"],
		"agent_version": agent["version"], "job_id": jobID, "workspace_id": workspaceID,
	}
}

// Older revisions predate explicit attribution. An exact material_saved event
// is evidence of authorship; a mention, tool argument, or proposed edit is not.
// Enrich only the API response, leaving every stored historical snapshot intact.
func (s *Store) resolveLegacyProfileOrigins(id string, versions []Doc) {
	missing := map[int]Doc{}
	for _, version := range versions {
		if version["revision_origin"] == nil {
			missing[integer(version["version"])] = version
		}
	}
	if len(missing) == 0 || count(s.db, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='generation_jobs'") == 0 {
		return
	}
	for _, row := range rows(s.db, `SELECT
 json_extract(e.value,'$.material.version') AS version,
 json_object('id',json_extract(g.doc,'$.agent.id'),
             'name',json_extract(g.doc,'$.agent.name'),
             'version',json_extract(g.doc,'$.agent.version')) AS agent,
 g.id AS job_id, json_extract(g.doc,'$.workspace_id') AS workspace_id
 FROM generation_jobs g, json_each(g.doc,'$.events') e
 WHERE json_extract(e.value,'$.stage')='material_saved'
 AND json_extract(e.value,'$.material.kind')='profile'
 AND json_extract(e.value,'$.material.id')=? ORDER BY g.rowid`, id) {
		if version := missing[integer(row["version"])]; version != nil {
			version["revision_origin"] = agentProfileOrigin(decode(str(row["agent"])), str(row["job_id"]), str(row["workspace_id"]))
			delete(missing, integer(row["version"]))
		}
	}
}
