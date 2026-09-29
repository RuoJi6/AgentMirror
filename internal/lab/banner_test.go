package lab

import (
	"net"
	"strings"
	"testing"
)

func TestBannerOnlyAdvertisesRunningListeners(t *testing.T) {
	b := newLab(t)
	if !strings.Contains(b.app.Banner(), "管理端 1 个，蜜罐端口 0 个") || !strings.Contains(b.app.Banner(), "公告地址 暂无") || strings.Contains(b.app.Banner(), b.app.PublicURL()) {
		t.Fatal("fresh banner advertises an unopened endpoint", b.app.Banner())
	}
	deployment := b.deployment()
	active := b.listener(deployment)
	paused := b.listener(deployment)
	paused["enabled"] = false
	b.legacyListener(paused)
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	failed := clone(active)
	merge(failed, Doc{"id": "blocked-banner", "port": port, "public_url": "http://127.0.0.1:" + fmtPort(port)})
	b.app.store.write(func(q queryer) { put(q, "listeners", failed) })
	b.restart()
	banner := b.app.Banner()
	if !strings.Contains(banner, "管理端 1 个，蜜罐端口 1 个") || !strings.Contains(banner, str(active["public_url"])) {
		t.Fatal("missing active endpoint", banner)
	}
	if strings.Contains(banner, str(paused["public_url"])) || strings.Contains(banner, str(failed["public_url"])) || !strings.Contains(banner, "另有 2 个蜜罐端口未运行") {
		t.Fatal("inactive endpoint advertised", banner)
	}
}

func TestRetireUnconfiguredDevelopmentAddress(t *testing.T) {
	b := newLab(t)
	legacy := "http://10.211.55.2:8765"
	b.app.store.write(func(q queryer) { set := settings(q); set["public_url"] = legacy; putSettings(q, set) })
	b.opts.PublicPort = b.port()
	b.restart()
	if b.app.PublicURL() != "http://127.0.0.1:"+fmtPort(b.opts.PublicPort) || strings.Contains(b.app.Banner(), legacy) {
		t.Fatal("old placeholder survived without a public listener")
	}
	custom := "http://example.test:9876"
	b.app.store.write(func(q queryer) { set := settings(q); set["public_url"] = custom; putSettings(q, set) })
	b.restart()
	if b.app.PublicURL() != custom {
		t.Fatal("custom saved address was changed")
	}
	b.app.store.write(func(q queryer) {
		set := settings(q)
		set["public_url"] = legacy
		putSettings(q, set)
		put(q, "listeners", Doc{"id": "existing", "role": "public", "name": "Existing paused port", "host": "127.0.0.1", "port": 8765, "public_url": legacy, "enabled": false})
	})
	b.restart()
	if b.app.PublicURL() != legacy || str(entityMaybe(b.app.store.db, "listeners", "existing")["public_url"]) != legacy {
		t.Fatal("configured endpoint was changed")
	}
	if strings.Contains(b.app.Banner(), legacy) {
		t.Fatal("paused endpoint advertised")
	}
}
