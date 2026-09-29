package lab

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

func fmtPort(port int) string { return strconv.Itoa(port) }

type runningServer struct {
	server   *http.Server
	listener net.Listener
	host     string
	port     int
}
type listenerManager struct {
	mu      sync.Mutex
	app     *App
	admin   Doc // Immutable startup configuration; never stored as a test listener.
	servers map[string]*runningServer
	errors  map[string]string
	retired sync.WaitGroup
	closed  bool
}

func (m *listenerManager) initialize(opts Options) {
	addressHost := opts.AdminHost
	if addressHost == "0.0.0.0" {
		addressHost = "127.0.0.1"
	}
	m.admin = Doc{"id": "admin", "role": "admin", "host": opts.AdminHost, "port": opts.AdminPort, "public_url": "http://" + net.JoinHostPort(addressHost, fmtPort(opts.AdminPort))}
	m.app.store.write(func(q queryer) {
		set := settings(q)
		if !boolean(set["ports_unified"]) {
			now := timestamp()
			set["default_listener"] = nil
			if str(set["default_deployment"]) != "" {
				primary := Doc{"id": "primary", "role": "public", "name": "默认测试入口", "host": opts.Host, "port": opts.PublicPort, "public_url": set["public_url"], "deployment_id": set["default_deployment"], "root_surface": "page", "enabled": true, "overrides": nil, "legacy_paths": true, "created_at": now, "updated_at": now}
				put(q, "listeners", primary)
				set["default_listener"] = "primary"
			}
			set["ports_unified"] = true
			putSettings(q, set)
		}
		for _, d := range listing(q, "listeners") {
			if d["id"] == "admin" || d["role"] == "admin" {
				// Remove the former UI-managed administration entry on upgrade.
				exec(q, "DELETE FROM entities WHERE kind=? AND id=?", "listeners", d["id"])
			} else if integer(d["port"]) == opts.AdminPort {
				fail(409, "管理后台启动失败：端口已被测试端口配置占用，请修改 --admin-port")
			}
		}
		// Retire the old development-machine placeholder only for databases
		// without any configured public listener. Preserve existing endpoints.
		if count(q, "SELECT COUNT(*) FROM entities WHERE kind='listeners'") == 0 && str(set["public_url"]) == "http://10.211.55.2:8765" {
			set["public_url"] = opts.PublicURL
			putSettings(q, set)
		}
		syncDefault(q)
	})
}
func syncDefault(q queryer) {
	set := settings(q)
	if !boolean(set["ports_unified"]) {
		return
	}
	var first, enabled, selected Doc
	for _, l := range listing(q, "listeners") {
		if l["role"] == "admin" {
			continue
		}
		if first == nil {
			first = l
		}
		if enabled == nil && boolean(l["enabled"]) {
			enabled = l
		}
		if l["id"] == set["default_listener"] {
			selected = l
		}
	}
	if selected == nil {
		selected = enabled
	}
	if selected == nil {
		selected = first
	}
	set["default_listener"] = nil
	set["default_deployment"] = nil
	if selected != nil {
		set["default_listener"] = selected["id"]
		set["default_deployment"] = selected["deployment_id"]
		set["public_url"] = selected["public_url"]
	}
	putSettings(q, set)
}
func (m *listenerManager) runtime() Doc {
	q := m.app.store.db
	set := settings(q)
	admin := m.admin
	result := Doc{"backend": "go", "version": Version, "db_path": m.app.store.path, "public_host": nil, "public_port": nil, "admin_host": admin["host"], "admin_port": admin["port"]}
	for _, l := range listing(q, "listeners") {
		if l["id"] == set["default_listener"] {
			result["public_host"] = l["host"]
			result["public_port"] = l["port"]
		}
	}
	return result
}
func (m *listenerManager) bind(doc Doc) *runningServer {
	host, port := str(doc["host"]), integer(doc["port"])
	ln, err := net.Listen("tcp4", net.JoinHostPort(host, fmtPort(port)))
	if err != nil {
		fail(409, fmt.Sprintf("无法监听 %s:%d：%v", host, port, err))
	}
	server := &http.Server{Handler: m.app.handler(doc["role"] == "admin", str(doc["id"]), port), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024, ErrorLog: log.New(io.Discard, "", 0)}
	return &runningServer{server, ln, host, port}
}
func (m *listenerManager) start(id string, server *runningServer) {
	m.servers[id] = server
	delete(m.errors, id)
	go func() {
		err := server.server.Serve(server.listener)
		if err != nil && err != http.ErrServerClosed {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.servers[id] == server {
				delete(m.servers, id)
				m.errors[id] = "监听异常停止"
			}
		}
	}()
}

// Stop accepting new connections immediately, then drain requests in flight.
func (m *listenerManager) retire(server *runningServer) {
	server.listener.Close()
	server.server.SetKeepAlivesEnabled(false)
	m.retired.Add(1)
	go func() {
		defer m.retired.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.server.Shutdown(ctx) != nil {
			server.server.Close()
		}
	}()
}
func (m *listenerManager) listingLocked() []Doc {
	defaultID := settings(m.app.store.db)["default_listener"]
	result := []Doc{}
	for _, doc := range listing(m.app.store.db, "listeners") {
		doc["role"] = fallback(doc["role"], "public")
		doc["is_default"] = doc["id"] == defaultID
		id := str(doc["id"])
		status, errText := "stopped", ""
		if m.servers[id] != nil {
			status = "running"
		} else if boolean(doc["enabled"]) {
			status = "error"
			errText = "监听尚未启动"
		}
		if e := m.errors[id]; e != "" {
			errText = e
		}
		doc["status"] = status
		doc["error"] = errText
		result = append(result, doc)
	}
	return result
}
func (m *listenerManager) listing() []Doc { m.mu.Lock(); defer m.mu.Unlock(); return m.listingLocked() }
func (m *listenerManager) save(raw Doc) Doc {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(managedListenerConfig(m.app.store.db, raw))
}
func (m *listenerManager) saveLocked(raw Doc, guards ...func(queryer)) Doc {
	if m.closed {
		fail(503, "服务正在停止")
	}
	var prepared *runningServer
	committed := false
	defer func() {
		if prepared != nil && !committed {
			prepared.listener.Close()
		}
	}()
	var doc Doc
	var current *runningServer
	changed := false
	m.app.store.write(func(q queryer) {
		for _, guard := range guards {
			guard(q)
		}
		doc = prepareListener(q, raw, integer(m.admin["port"]))
		current = m.servers[str(doc["id"])]
		changed = current != nil && (current.host != str(doc["host"]) || current.port != integer(doc["port"]))
		if changed && boolean(doc["enabled"]) && current.port == integer(doc["port"]) {
			fail(409, "修改正在运行的监听地址前，请先暂停此端口")
		}
		if boolean(doc["enabled"]) && (current == nil || changed) {
			prepared = m.bind(doc)
		}
		put(q, "listeners", doc)
		syncDefault(q)
	})
	committed = true
	id := str(doc["id"])
	if prepared != nil {
		m.start(id, prepared)
	}
	if current != nil && (changed || !boolean(doc["enabled"])) {
		if m.servers[id] == current {
			delete(m.servers, id)
		}
		m.retire(current)
	}
	delete(m.errors, id)
	for _, result := range m.listingLocked() {
		if result["id"] == id {
			return result
		}
	}
	panic("saved listener missing")
}
func (m *listenerManager) saveSettings(raw Doc) Doc {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := settings(m.app.store.db)
	id := str(set["default_listener"])
	if id == "" {
		fail(400, "请先在多端口管理中新建测试端口")
	}
	doc := get(m.app.store.db, "listeners", id)
	if doc["deployment_id"] != raw["default_deployment"] {
		doc["overrides"], doc["root_surface"], doc["site_node_id"] = nil, "page", workspaceRootSite
	}
	merge(doc, Doc{"public_url": raw["public_url"], "deployment_id": raw["default_deployment"]})
	m.saveLocked(managedListenerConfig(m.app.store.db, doc))
	return settings(m.app.store.db)
}
func (m *listenerManager) setDefault(id string) {
	if id == "admin" {
		fail(400, "管理后台不属于测试端口")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.app.store.write(func(q queryer) {
		doc := get(q, "listeners", id)
		if doc["role"] == "admin" || !boolean(doc["enabled"]) {
			fail(400, "请选择已启用的测试端口")
		}
		set := settings(q)
		set["default_listener"] = id
		putSettings(q, set)
		syncDefault(q)
	})
}
func (m *listenerManager) delete(id string, guards ...func(queryer)) {
	if id == "admin" {
		fail(400, "管理后台仅通过启动参数配置")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.app.store.write(func(q queryer) {
		for _, guard := range guards {
			guard(q)
		}
		doc := get(q, "listeners", id)
		if doc["role"] == "admin" {
			fail(400, "管理端口不能删除，以保留配置入口")
		}
		exec(q, "DELETE FROM entities WHERE kind=? AND id=?", "listeners", id)
		syncDefault(q)
	})
	if current := m.servers[id]; current != nil {
		delete(m.servers, id)
		m.retire(current)
	}
	delete(m.errors, id)
}
func (m *listenerManager) restore() {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Bind administration first; a public conflict remains visible in the UI.
	m.start("admin", m.bind(m.admin))
	for _, doc := range listing(m.app.store.db, "listeners") {
		if !boolean(doc["enabled"]) {
			continue
		}
		func() {
			defer func() {
				if p := recover(); p != nil {
					if e, ok := p.(problem); ok {
						m.errors[str(doc["id"])] = e.message
					} else {
						panic(p)
					}
				}
			}()
			m.start(str(doc["id"]), m.bind(doc))
		}()
	}
}
func (m *listenerManager) close() {
	m.mu.Lock()
	m.closed = true
	for id, server := range m.servers {
		delete(m.servers, id)
		m.retire(server)
	}
	m.mu.Unlock()
	m.retired.Wait()
}
