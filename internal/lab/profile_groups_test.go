package lab

import (
	"encoding/json"
	"testing"
)

func TestProfileGroupsAndLegacyVersions(t *testing.T) {
	b := newLab(t)
	for _, category := range []string{"command_execution", "ethical_blocking", "unexpected_output"} {
		created := b.api("/profiles", "POST", Doc{"name": "group fixture", "category": category, "body": "SYNTHETIC {{result.output}}"}, 200)
		if created["category"] != category {
			t.Fatalf("category not stored: %v", created)
		}
		versions := b.request(b.admin, "/api/profiles/"+str(created["id"])+"/versions", "GET", nil)
		var history []Doc
		if versions.status != 200 || json.Unmarshal(versions.body, &history) != nil || len(history) != 1 || history[0]["category"] != category {
			t.Fatal("category missing from history")
		}
	}
	b.api("/profiles", "POST", Doc{"name": "invalid", "category": "unknown", "body": "SYNTHETIC"}, 400)

	legacy := get(b.app.store.db, "profiles", "environment")
	delete(legacy, "category")
	originalBody, originalVersion := legacy["body"], legacy["version"]
	legacy["category"] = "ethical_blocking"
	delete(legacy, "level")
	saved := b.api("/profiles", "POST", legacy, 200)
	if saved["body"] != originalBody || saved["level"] != "L2" || integer(saved["version"]) != integer(originalVersion)+1 {
		t.Fatalf("legacy profile not preserved: %v", saved)
	}
	var firstVersion string
	check(b.app.store.db.QueryRow("SELECT doc FROM versions WHERE profile_id=? AND version=?", "environment", originalVersion).Scan(&firstVersion))
	if decode(firstVersion)["body"] != originalBody {
		t.Fatal("historical body was changed")
	}
}
