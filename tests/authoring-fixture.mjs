import assert from "node:assert/strict";

// Authoring tests own a fresh temporary database and need an empty workspace.
// The production defaults are checked before removing them through normal APIs.
export async function clearBuiltinTestMaterials(request, admin) {
  assert.equal(new URL(admin).hostname, "127.0.0.1");
  const response = await request.get(admin + "/api/state");
  assert.equal(response.status(), 200);
  const state = await response.json();
  assert.equal(state.workspaces.length, 12);
  assert.equal(state.profiles.length, 12);
  assert.equal(state.deployments.length, 0);
  for (const kind of ["workspaces", "scenarios", "sites", "profiles"]) {
    let items = state.profiles;
    if (kind !== "profiles") {
      const response = await request.get(admin + "/api/" + kind);
      assert.equal(response.status(), 200);
      items = await response.json();
    }
    for (const item of items) {
      const result = await request.delete(admin + "/api/" + kind + "/" + item.id, {
        headers: { "X-Admin-Token": state.csrf },
      });
      assert.equal(result.status(), 200, await result.text());
    }
  }
}
