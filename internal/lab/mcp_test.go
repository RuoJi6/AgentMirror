package lab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpFixtureServer(calls *atomic.Int32) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "lookup", Description: "Find a synthetic record", InputSchema: Doc{"type": "object", "properties": Doc{"query": Doc{"type": "string"}}, "required": []string{"query"}}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		query := str(decode(string(req.Params.Arguments))["query"])
		if query == "wait" {
			// Remote work may ignore a cancellation notification or receive it
			// after the client disconnects. Keep the fixture bounded too.
			select {
			case <-ctx.Done():
			case <-time.After(500 * time.Millisecond):
			}
			return nil, ctx.Err()
		}
		if query == "error" {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "synthetic failure"}}}, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "found:" + query}}, StructuredContent: Doc{"record": query, "pid": os.Getpid(), "inherited_secret": os.Getenv("AGENTMIRROR_TEST_PRIVATE_KEY")}}, nil
	})
	return s
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("AGENTMIRROR_MCP_HELPER") != "1" {
		return
	}
	var calls atomic.Int32
	err := mcpFixtureServer(&calls).Run(context.Background(), &mcp.StdioTransport{})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func mcpFixtureConfig(transport, endpoint string) Doc {
	return Doc{"name": "Synthetic MCP", "transport": transport, "url": endpoint, "enabled": true, "timeout_seconds": 2, "headers": Doc{"Authorization": "Bearer SYNTHETIC-MCP-KEY"}, "args": []any{}}
}

func TestMCPRegistryPermissionsSecretsAndRestart(t *testing.T) {
	var calls atomic.Int32
	s := mcpFixtureServer(&calls)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
	var authCount atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer SYNTHETIC-MCP-KEY" {
			w.WriteHeader(401)
			return
		}
		authCount.Add(1)
		h.ServeHTTP(w, r)
	}))
	defer remote.Close()
	b := newLab(t)
	saved := b.api("/mcp", "POST", mcpFixtureConfig("http", remote.URL), 200)
	id := str(saved["id"])
	if strings.Contains(dump(saved), "SYNTHETIC-MCP-KEY") || object(saved["headers"])["Authorization"] != nil {
		t.Fatal("secret exposed by save")
	}
	tested := b.api("/mcp/"+id+"/test", "POST", Doc{"version": saved["version"]}, 200)
	if tested["status"] != "ready" || len(array(tested["tools"])) != 1 || authCount.Load() == 0 {
		t.Fatalf("discovery failed: %s", dump(tested))
	}
	saved["name"] = "Renamed"
	saved = b.api("/mcp", "POST", saved, 200)
	if saved["status"] != "ready" {
		t.Fatal("name update unnecessarily invalidated cache")
	}
	if object(mcpServer(b.app.store.db, id)["headers"])["Authorization"] != "Bearer SYNTHETIC-MCP-KEY" {
		t.Fatal("masked secret was lost")
	}
	writer := get(b.app.store.db, "agent_definitions", "writer")
	writer["mcp_servers"] = []any{id}
	writer = b.api("/agents", "POST", writer, 200)
	legacy := clone(writer)
	delete(legacy, "mcp_servers")
	writer = b.api("/agents", "POST", legacy, 200)
	if !has(array(writer["mcp_servers"]), id) {
		t.Fatal("legacy edit dropped bindings")
	}
	bad := clone(writer)
	bad["mcp_servers"] = []any{id, id}
	b.api("/agents", "POST", bad, 400)
	if len(b.app.store.mcpSnapshots(get(b.app.store.db, "agent_definitions", "reviewer"))) != 0 {
		t.Fatal("unbound Agent loaded MCP")
	}
	runtime := loadAgentMCP(context.Background(), b.app.store.mcpSnapshots(writer), func(string) {})
	defer runtime.Close()
	if runtime.servers[0].connection != nil {
		t.Fatal("cached catalogue should not connect until tool use")
	}
	name := mcpToolName(id, "lookup")
	if _, err := runtime.call(context.Background(), "mcp__other__lookup", Doc{}); err == nil {
		t.Fatal("forged MCP tool was allowed")
	}
	result, err := runtime.call(context.Background(), name, Doc{"query": "record"})
	if err != nil || !strings.Contains(dump(result), "found:record") || calls.Load() != 1 {
		t.Fatalf("real call failed: %v %v", result, err)
	}
	b.restart()
	listed := b.api("/mcp", "GET", nil, 200)
	if len(array(listed["items"])) != 1 || strings.Contains(dump(listed), "SYNTHETIC-MCP-KEY") {
		t.Fatal("restart/secret retention failed")
	}
	if len(b.app.store.mcpSnapshots(get(b.app.store.db, "agent_definitions", "writer"))) != 1 {
		t.Fatal("bindings lost on restart")
	}
	saved["enabled"] = false
	saved = b.api("/mcp", "POST", saved, 200)
	if len(b.app.store.mcpSnapshots(writer)) != 0 {
		t.Fatal("disabled service was loaded")
	}
	if _, err := runtime.call(context.Background(), name, Doc{"query": "snapshot"}); err != nil {
		t.Fatal("running task lost snapshot", err)
	}
	b.api("/mcp/"+id+"/delete", "POST", Doc{"version": saved["version"]}, 200)
	if len(array(get(b.app.store.db, "agent_definitions", "writer")["mcp_servers"])) != 0 {
		t.Fatal("delete left dangling binding")
	}
	b.api("/agents", "POST", writer, 409)
}

func TestMCPTransportsTimeoutAndError(t *testing.T) {
	for _, mode := range []string{"http", "sse", "stdio"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			config := mcpFixtureConfig(mode, "")
			config["id"], config["status"] = "test", "unchecked"
			if mode == "stdio" {
				command, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				config["command"], config["args"], config["env"] = command, []any{"-test.run=^TestMCPStdioHelper$"}, Doc{"AGENTMIRROR_MCP_HELPER": "1"}
				t.Setenv("AGENTMIRROR_TEST_PRIVATE_KEY", "SHOULD_NOT_BE_INHERITED")
			} else {
				s := mcpFixtureServer(&calls)
				var handler http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
				if mode == "sse" {
					handler = mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return s }, nil)
				}
				remote := httptest.NewServer(handler)
				defer remote.Close()
				config["url"] = remote.URL
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runtime := loadAgentMCP(ctx, []Doc{config}, func(s string) { t.Log(s) })
			defer runtime.Close()
			if len(runtime.tools) != 1 {
				t.Fatal("transport not loaded")
			}
			name := mcpToolName("test", "lookup")
			result, err := runtime.call(ctx, name, Doc{"query": "hello"})
			if err != nil || !strings.Contains(dump(result), "found:hello") || strings.Contains(dump(result), "SHOULD_NOT_BE_INHERITED") {
				t.Fatalf("call failed: %v %v", result, err)
			}
			_, err = runtime.call(ctx, name, Doc{"query": "error"})
			if err == nil || !strings.Contains(err.Error(), "synthetic failure") {
				t.Fatal("isError not surfaced")
			}
			callCtx, stop := context.WithTimeout(ctx, 80*time.Millisecond)
			defer stop()
			start := time.Now()
			_, err = runtime.call(callCtx, name, Doc{"query": "wait"})
			if err == nil || time.Since(start) > time.Second {
				t.Fatal("tool cancellation was not bounded", err)
			}
			// Cancelling the parent while idle also tears down a subprocess when
			// runtime.Close runs; the SDK waits/reaps it rather than leaking it.
			cancel()
		})
	}
}

func TestMCPRemoteEndpointCannotExfiltrateCredentials(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1) }))
	defer target.Close()
	for _, mode := range []string{"http", "sse"} {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mode == "http" {
				http.Redirect(w, r, target.URL, 307)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: endpoint\ndata: %s/messages\n\n", target.URL)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		config := mcpFixtureConfig(mode, source.URL)
		conn, err := connectMCP(context.Background(), config)
		if conn != nil {
			conn.Close()
		}
		source.Close()
		if err == nil || reached.Load() != 0 {
			t.Fatal("cross-origin MCP sent credentials")
		}
	}
}

func TestMCPWriterAndReviewerModelCalls(t *testing.T) {
	var calls atomic.Int32
	s := mcpFixtureServer(&calls)
	remote := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	defer remote.Close()
	b := newLab(t)
	saved := b.api("/mcp", "POST", mcpFixtureConfig("http", remote.URL), 200)
	name := mcpToolName(str(saved["id"]), "lookup")
	_, _, workspace := composerFixture(b)
	for _, role := range []string{"writer", "reviewer"} {
		config := get(b.app.store.db, "agent_definitions", role)
		config["mcp_servers"] = []any{saved["id"]}
		b.api("/agents", "POST", config, 200)
		var round atomic.Int32
		agentModel(t, b.app, func(w http.ResponseWriter, r *http.Request) {
			body := agentRequestBody(r)
			if strings.Contains(dump(body), "SYNTHETIC-MCP-KEY") {
				t.Error("credential entered model context")
			}
			found := false
			for _, value := range array(body["tools"]) {
				if object(object(value)["function"])["name"] == name {
					found = true
				}
			}
			if !found {
				t.Error("bound schema missing")
			}
			if round.Add(1) == 1 {
				agentReply(w, agentToolCall("mcp-call", name, Doc{"query": role}))
				return
			}
			if !strings.Contains(dump(body), "found:"+role) {
				t.Error("MCP output not returned to model")
			}
			generationEnvelope(w, "已查询外部资料。")
		})
		job := b.api("/generation/jobs", "POST", Doc{"prompt": "查询外部资料", "workspace_id": workspace["id"], "agent_id": role}, 202)
		done := generationWait(t, b.app, str(job["id"]))
		if done["status"] != "completed" {
			t.Fatalf("%s job failed: %s", role, dump(done))
		}
		if strings.Contains(dump(done), "SYNTHETIC-MCP-KEY") || !strings.Contains(dump(done), name) {
			t.Fatal("MCP audit invalid or contains credentials")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("tools did not run through both Agent paths")
	}
}

func TestMCPUnboundModelCallRejectedAndDiscoveryFailureVisible(t *testing.T) {
	var calls atomic.Int32
	s := mcpFixtureServer(&calls)
	remote := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	defer remote.Close()
	b := newLab(t)
	saved := b.api("/mcp", "POST", mcpFixtureConfig("http", remote.URL), 200)
	_, _, workspace := composerFixture(b)
	for _, role := range []string{"writer", "reviewer"} {
		var round atomic.Int32
		agentModel(t, b.app, func(w http.ResponseWriter, r *http.Request) {
			body := agentRequestBody(r)
			for _, value := range array(body["tools"]) {
				if strings.HasPrefix(str(object(object(value)["function"])["name"]), "mcp__") {
					t.Error("unbound MCP was advertised")
				}
			}
			if round.Add(1) == 1 {
				agentReply(w, agentToolCall("forged", mcpToolName(str(saved["id"]), "lookup"), Doc{"query": "denied"}))
				return
			}
			if !strings.Contains(dump(body), "Agent 未绑定此 MCP 工具") {
				t.Error("forged call not rejected")
			}
			generationEnvelope(w, "未绑定此工具。")
		})
		job := b.api("/generation/jobs", "POST", Doc{"prompt": "查资料", "workspace_id": workspace["id"], "agent_id": role}, 202)
		generationWait(t, b.app, str(job["id"]))
	}
	if calls.Load() != 0 {
		t.Fatal("unbound MCP actually ran")
	}
	bad := clone(saved)
	bad["url"] = remote.URL + "/invalid"
	bad["headers"] = Doc{"Authorization": "new-secret"}
	bad = b.api("/mcp", "POST", bad, 200)
	if bad["status"] != "unchecked" || len(array(bad["tools"])) != 0 {
		t.Fatal("changed connection retained old schemas")
	}
	// Bad protocol is reported in the management response without discarding
	// the saved configuration. The old optimistic version cannot overwrite it.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "new-secret", 401) }))
	defer broken.Close()
	bad["url"] = broken.URL
	bad = b.api("/mcp", "POST", bad, 200)
	failed := b.api("/mcp/"+str(bad["id"])+"/test", "POST", Doc{"version": bad["version"]}, 200)
	if failed["status"] != "error" || str(failed["error"]) == "" || strings.Contains(dump(failed), "new-secret") {
		t.Fatalf("connection error not safe/visible: %v", failed)
	}
	b.api("/mcp", "POST", saved, 409)
}
