package lab

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Provider credentials live outside exported entities and job documents. The
// selection/default split follows ARTEX's profile/runtime design, independently
// implemented here without its SDK or task execution capabilities.
func initGenerationProviders(store *Store) {
	store.write(func(q queryer) {
		exec(q, `CREATE TABLE IF NOT EXISTS generation_providers(id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL, base_url TEXT NOT NULL, model TEXT NOT NULL, api_key TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS generation_provider_state(id INTEGER PRIMARY KEY CHECK(id=1), default_provider_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS generation_provider_options(provider_id TEXT PRIMARY KEY, request_timeout_seconds INTEGER NOT NULL, job_timeout_seconds INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS generation_provider_streaming(provider_id TEXT PRIMARY KEY, enabled INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS generation_provider_limits(provider_id TEXT PRIMARY KEY, context_window_tokens INTEGER NOT NULL, max_output_tokens INTEGER NOT NULL);`)
		if count(q, "SELECT COUNT(*) FROM generation_provider_state") != 0 {
			return
		}
		var base, model, key string
		check(q.QueryRow("SELECT base_url,model,api_key FROM generation_secrets WHERE id=1").Scan(&base, &model, &key))
		id := ""
		if base != "" && model != "" {
			id = randomHex(12)
			exec(q, "INSERT INTO generation_providers VALUES(?,?,?,?,?,?)", id, "原有模型配置", "openai", base, model, key)
		}
		// The marker prevents deleted providers from being resurrected on restart.
		exec(q, "INSERT INTO generation_provider_state VALUES(1,?)", id)
	})
}

func generationDefaultProvider(q queryer) string {
	var id string
	check(q.QueryRow("SELECT default_provider_id FROM generation_provider_state WHERE id=1").Scan(&id))
	return id
}

func generationProvider(q queryer, id string, secret bool) Doc {
	var name, protocol, base, model, key string
	err := q.QueryRow("SELECT name,protocol,base_url,model,api_key FROM generation_providers WHERE id=?", id).Scan(&name, &protocol, &base, &model, &key)
	if err == sql.ErrNoRows {
		fail(404, "模型供应商配置不存在，请重新选择")
	}
	check(err)
	d := Doc{"id": id, "name": name, "protocol": protocol, "base_url": base, "model": model, "has_key": key != ""}
	d["request_timeout_seconds"] = int(generationRequestTimeout.Seconds())
	d["job_timeout_seconds"] = int(generationJobTimeout.Seconds())
	for _, row := range rows(q, "SELECT request_timeout_seconds,job_timeout_seconds FROM generation_provider_options WHERE provider_id=?", id) {
		for key, value := range row {
			d[key] = value
		}
	}
	for _, field := range []string{"context_window_tokens", "max_output_tokens"} {
		d[field] = generationTokenLimit(nil, field)
	}
	for _, row := range rows(q, "SELECT context_window_tokens,max_output_tokens FROM generation_provider_limits WHERE provider_id=?", id) {
		for key, value := range row {
			d[key] = value
		}
	}
	d["streaming"] = true
	for _, row := range rows(q, "SELECT enabled FROM generation_provider_streaming WHERE provider_id=?", id) {
		d["streaming"] = integer(row["enabled"]) != 0
	}
	if secret {
		d["api_key"] = key
	}
	return d
}

func generationProviderAudit(settings Doc) Doc {
	d := Doc{}
	for _, field := range []string{"id", "name", "protocol", "model", "request_timeout_seconds", "job_timeout_seconds", "context_window_tokens", "max_output_tokens", "streaming"} {
		d[field] = settings[field]
	}
	return d
}

func syncLegacyGenerationSettings(q queryer) {
	base, model, key := "", "", ""
	if id := generationDefaultProvider(q); id != "" {
		d := generationProvider(q, id, true)
		base, model, key = str(d["base_url"]), str(d["model"]), str(d["api_key"])
	}
	exec(q, "UPDATE generation_secrets SET base_url=?,model=?,api_key=? WHERE id=1", base, model, key)
}

func (m *generationManager) listProviders() Doc {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := []Doc{}
	for _, row := range rows(m.store.db, "SELECT id FROM generation_providers ORDER BY rowid") {
		items = append(items, generationProvider(m.store.db, str(row["id"]), false))
	}
	return Doc{"items": items, "default_provider_id": generationDefaultProvider(m.store.db)}
}

func (m *generationManager) saveProvider(raw Doc, legacy bool) Doc {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result Doc
	m.store.write(func(q queryer) {
		id := textField(raw, "id", 64, false)
		if legacy {
			id = generationDefaultProvider(q)
		}
		old := Doc{}
		if id != "" {
			if !idPattern.MatchString(id) {
				fail(400, "供应商配置编号无效")
			}
			old = generationProvider(q, id, true)
		} else {
			if count(q, "SELECT COUNT(*) FROM generation_providers") >= 32 {
				fail(400, "最多保存 32 个供应商配置")
			}
			id = randomHex(12)
		}
		d := validateGenerationSettings(raw, old)
		name := optional(raw, "name", old["name"])
		if legacy && str(name) == "" {
			name = "默认模型配置"
		}
		d["name"] = textField(Doc{"name": name}, "name", 100, true)
		d["id"] = id
		exec(q, `INSERT INTO generation_providers VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,protocol=excluded.protocol,base_url=excluded.base_url,model=excluded.model,api_key=excluded.api_key`, id, d["name"], d["protocol"], d["base_url"], d["model"], d["api_key"])
		exec(q, `INSERT INTO generation_provider_options VALUES(?,?,?) ON CONFLICT(provider_id) DO UPDATE SET request_timeout_seconds=excluded.request_timeout_seconds,job_timeout_seconds=excluded.job_timeout_seconds`, id, d["request_timeout_seconds"], d["job_timeout_seconds"])
		exec(q, `INSERT INTO generation_provider_limits VALUES(?,?,?) ON CONFLICT(provider_id) DO UPDATE SET context_window_tokens=excluded.context_window_tokens,max_output_tokens=excluded.max_output_tokens`, id, d["context_window_tokens"], d["max_output_tokens"])
		exec(q, `INSERT INTO generation_provider_streaming VALUES(?,?) ON CONFLICT(provider_id) DO UPDATE SET enabled=excluded.enabled`, id, d["streaming"])
		if legacy || boolean(raw["set_default"]) || generationDefaultProvider(q) == "" {
			exec(q, "UPDATE generation_provider_state SET default_provider_id=? WHERE id=1", id)
		}
		syncLegacyGenerationSettings(q)
		delete(d, "api_key")
		d["default_provider_id"] = generationDefaultProvider(q)
		result = d
	})
	return result
}

func (m *generationManager) saveLegacySettings(raw Doc) Doc {
	return m.saveProvider(raw, true)
}

func (m *generationManager) deleteProvider(id string) Doc {
	m.mu.Lock()
	defer m.mu.Unlock()
	defaultID := ""
	m.store.write(func(q queryer) {
		generationProvider(q, id, false)
		exec(q, "DELETE FROM generation_providers WHERE id=?", id)
		exec(q, "DELETE FROM generation_provider_options WHERE provider_id=?", id)
		exec(q, "DELETE FROM generation_provider_limits WHERE provider_id=?", id)
		exec(q, "DELETE FROM generation_provider_streaming WHERE provider_id=?", id)
		defaultID = generationDefaultProvider(q)
		if defaultID == id {
			defaultID = ""
			for _, row := range rows(q, "SELECT id FROM generation_providers ORDER BY rowid LIMIT 1") {
				defaultID = str(row["id"])
			}
			exec(q, "UPDATE generation_provider_state SET default_provider_id=? WHERE id=1", defaultID)
		}
		syncLegacyGenerationSettings(q)
	})
	return Doc{"ok": true, "default_provider_id": defaultID}
}

func (m *generationManager) providerRoute(w http.ResponseWriter, r *http.Request, path string) bool {
	if path == "providers" {
		switch r.Method {
		case "GET":
			response(w, r, m.listProviders(), 200, "", nil)
		case "POST":
			response(w, r, m.saveProvider(generationBody(w, r), false), 200, "", nil)
		default:
			fail(405, "请求方法不支持")
		}
		return true
	}
	parts := strings.Split(strings.TrimPrefix(path, "providers/"), "/")
	if len(parts) > 2 || !idPattern.MatchString(parts[0]) {
		fail(404, "模型供应商配置不存在")
	}
	if len(parts) == 1 && r.Method == "DELETE" {
		response(w, r, m.deleteProvider(parts[0]), 200, "", nil)
		return true
	}
	if len(parts) == 2 && parts[1] == "test" && r.Method == "POST" {
		// Snapshot before network I/O; a slow test does not lock configuration/jobs.
		settings := generationProvider(m.store.db, parts[0], true)
		timeout := generationTimeout(settings, "request_timeout_seconds")
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(timeout + 5*time.Second))
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		started := time.Now()
		_, err := generationCompletion(ctx, settings, []Doc{{"role": "user", "content": "Reply with OK."}}, 512)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				fail(502, fmt.Sprintf("连接测试超时（%d 秒），可调整单次请求超时后重试", int(timeout.Seconds())))
			}
			fail(502, err.Error())
		}
		response(w, r, Doc{"ok": true, "model": settings["model"], "protocol": settings["protocol"], "duration_ms": time.Since(started).Milliseconds()}, 200, "", nil)
		return true
	}
	fail(405, "请求方法不支持")
	return true
}
