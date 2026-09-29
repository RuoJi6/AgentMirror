package lab

const maxGenerationAttachments = 8

// Binary contents stay in blob storage, outside requests to the model and job history.
func (m *generationManager) resolveAttachments(raw Doc, inherited any) []any {
	values := inherited
	if value, supplied := raw["attachments"]; supplied {
		if array(value) == nil {
			fail(400, "attachments 必须为数组")
		}
		values = value
	}
	if len(array(values)) > maxGenerationAttachments {
		fail(400, "每轮最多附加 8 个文件")
	}
	items := []any{}
	seen := map[string]bool{}
	for _, value := range array(values) {
		id := textField(object(value), "id", 64, true)
		if !idPattern.MatchString(id) {
			fail(400, "附件编号无效")
		}
		if seen[id] {
			continue
		}
		meta := get(m.store.db, "hosted_files", id)
		items = append(items, Doc{"id": meta["id"], "name": meta["name"], "size": meta["size"], "sha256": meta["sha256"]})
		seen[id] = true
	}
	return items
}
