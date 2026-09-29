package lab

import (
	"database/sql"
	"fmt"
)

const maxGenerationReferences = 8
const maxReferenceSnapshotBytes = 16 << 20

func referenceEntityKind(kind string) string {
	switch kind {
	case "site":
		return "sites"
	case "scenario":
		return "scenarios"
	case "profile":
		return "profiles"
	}
	fail(400, "引用类型需为 site、scenario 或 profile")
	return ""
}

// Profiles are selected for later binding, not read as Agent instructions.
// Select their metadata in SQL so even the in-memory snapshot excludes bodies.
func generationReferenceEntity(q queryer, kind, id string) Doc {
	entityKind := referenceEntityKind(kind)
	var raw string
	query := "SELECT doc FROM entities WHERE kind=? AND id=?"
	if kind == "profile" {
		query = `SELECT json_object('id',id,'name',json_extract(doc,'$.name'),'version',json_extract(doc,'$.version'),'category',json_extract(doc,'$.category')) FROM entities WHERE kind=? AND id=?`
	}
	err := q.QueryRow(query, entityKind, id).Scan(&raw)
	if err == sql.ErrNoRows {
		fail(404, "引用素材不存在，请重新选择")
	}
	check(err)
	return decode(raw)
}

func generationReferenceMetadata(kind string, d Doc) Doc {
	meta := Doc{"kind": kind, "id": d["id"], "name": d["name"], "version": d["version"]}
	switch kind {
	case "site":
		meta["file_count"] = len(array(d["files"]))
	case "scenario":
		meta["rule_count"] = len(array(d["rules"]))
	case "profile":
		meta["category"] = d["category"]
	}
	return meta
}

func referenceKey(kind, id string) string { return kind + ":" + id }

// Resolve one immutable snapshot per turn, never model- or client-supplied
// names/content. Explicit [] clears inherited references; omission retains their
// identities and resolves the latest version for the new turn. An explicitly
// submitted version still requests optimistic version checking.
func (m *generationManager) resolveReferences(raw Doc, inherited any) ([]any, map[string]Doc) {
	values := inherited
	_, explicit := raw["references"]
	if v, present := raw["references"]; present {
		values = v
		if _, ok := v.([]any); !ok {
			if _, ok := v.([]Doc); !ok {
				fail(400, "references 必须为数组，使用 [] 清空引用")
			}
		}
	}
	items := array(values)
	if len(items) > maxGenerationReferences {
		fail(400, "一次任务最多引用 8 个素材")
	}
	metadata, snapshots := []any{}, map[string]Doc{}
	profiles, total := 0, 0
	for _, item := range items {
		ref := object(item)
		kind := textField(ref, "kind", 20, true)
		referenceEntityKind(kind)
		id := textField(ref, "id", 64, true)
		if !idPattern.MatchString(id) {
			fail(400, "引用素材编号不正确")
		}
		d := generationReferenceEntity(m.store.db, kind, id)
		if version, supplied := ref["version"]; supplied {
			n, valid := number(version)
			if !valid || n < 1 {
				fail(400, "引用版本需为正整数")
			}
			if explicit && int(n) != integer(d["version"]) {
				fail(409, "引用素材已更新，请重新选择最新版本")
			}
		}
		key := referenceKey(kind, id)
		if snapshots[key] != nil {
			continue
		}
		if kind == "profile" {
			profiles++
			if profiles > 1 {
				fail(400, "一次任务只能引用一个提示词方案")
			}
		}
		total += len(jsonBytes(d))
		if total > maxReferenceSnapshotBytes {
			fail(413, "引用素材总量超过 16 MiB，请减少站点引用或分步调整")
		}
		snapshots[key] = d
		metadata = append(metadata, generationReferenceMetadata(kind, d))
	}
	return metadata, snapshots
}

func (a *draftAgent) referenced(args Doc) (string, Doc, Doc) {
	kind := textField(args, "kind", 20, true)
	id := textField(args, "id", 64, true)
	d := a.referenceSnapshots[referenceKey(kind, id)]
	if d == nil {
		fail(403, "只能访问本轮用户明确引用的素材")
	}
	return kind, d, generationReferenceMetadata(kind, d)
}

func (a *draftAgent) readReference(args Doc) (Doc, error) {
	kind, d, meta := a.referenced(args)
	switch kind {
	case "site":
		if str(args["path"]) == "" {
			meta["site"] = (&draftAgent{site: d}).manifest()
			return meta, nil
		}
		page, err := readDraftFile(d, args)
		if err != nil {
			return nil, err
		}
		merge(meta, page)
	case "scenario":
		if len(jsonBytes(d)) > maxAgentContext/2 {
			return nil, fmt.Errorf("场景规则过大，请缩小素材后再引用")
		}
		meta["scenario"] = clone(d)
	case "profile":
		// No parameter (including path) can request the prompt body.
	}
	return meta, nil
}

func (a *draftAgent) useReference(args Doc) (Doc, error) {
	kind, d, meta := a.referenced(args)
	switch kind {
	case "site":
		a.site = clone(d)
		// A replacement site is no longer the previous clone's page. Keep URL
		// authority unchanged, but discard stale capture provenance/cache.
		a.reference, a.warnings = nil, nil
		a.cloned = map[string]bool{}
		return Doc{"ok": true, "reference": meta, "site": a.manifest()}, nil
	case "scenario":
		a.scenario = clone(d)
		return Doc{"ok": true, "reference": meta, "rules": len(array(d["rules"]))}, nil
	default:
		return nil, fmt.Errorf("提示词由用户选择并在采用时绑定，Agent 仅使用 {{prompt}} 槽位")
	}
}

func readDraftFile(site, args Doc) (Doc, error) {
	path := sitePath(textField(args, "path", 512, true))
	for _, value := range array(site["files"]) {
		f := object(value)
		if f["path"] != path {
			continue
		}
		if f["encoding"] == "hosted" {
			return Doc{"path": path, "encoding": "hosted", "file_id": f["file_id"], "download_name": f["download_name"], "note": "Hosted binary reference. Contents are stored separately; preserve file_id, do not rewrite as text."}, nil
		}
		if f["encoding"] == "base64" {
			return Doc{"path": path, "encoding": f["encoding"], "file_id": f["file_id"], "bytes": len(siteFileBytes(f)), "note": "Binary asset; preserve by path, do not rewrite as text."}, nil
		}
		text := []rune(str(f["content"]))
		offset, limit := integer(args["offset"]), integer(args["limit"])
		if offset < 0 || offset > len(text) {
			fail(400, "读取偏移无效")
		}
		if limit <= 0 {
			limit = 8000
		}
		if limit > 48000 {
			limit = 48000
		}
		end := offset + limit
		if end > len(text) {
			end = len(text)
		}
		return Doc{"path": path, "content": string(text[offset:end]), "offset": offset, "next_offset": end, "total_chars": len(text), "truncated": end < len(text), "sha256": draftFileHash(f)}, nil
	}
	return nil, fmt.Errorf("草稿文件不存在：%s", path)
}

// Recheck the user's exact selection inside the adoption transaction. Neither a
// model result nor the materialize request can choose a different profile.
func materializedReferenceProfile(q queryer, job Doc) Doc {
	for _, value := range array(job["references"]) {
		ref := object(value)
		if ref["kind"] != "profile" {
			continue
		}
		d := generationReferenceEntity(q, "profile", str(ref["id"]))
		if integer(d["version"]) != integer(ref["version"]) {
			fail(409, "引用的提示词方案已更新，请重新选择并生成草稿")
		}
		return generationReferenceMetadata("profile", d)
	}
	return nil
}
