package lab

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpTimeout(d Doc) time.Duration {
	return time.Duration(max(1, integer(d["timeout_seconds"]))) * time.Second
}

func redactMCP(d Doc, text string) string {
	for _, key := range []string{"headers", "env"} {
		for _, v := range object(d[key]) {
			if s := str(v); s != "" {
				text = strings.ReplaceAll(text, s, "[已隐藏]")
				if strings.HasPrefix(strings.ToLower(s), "bearer ") {
					text = strings.ReplaceAll(text, s[7:], "[已隐藏]")
				}
			}
		}
	}
	if u, err := url.Parse(str(d["url"])); err == nil {
		for _, values := range u.Query() {
			for _, value := range values {
				if value != "" {
					text = strings.ReplaceAll(text, value, "[已隐藏]")
					text = strings.ReplaceAll(text, url.QueryEscape(value), "[已隐藏]")
				}
			}
		}
	}
	return text
}

func safeMCPError(d Doc, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "MCP 请求已超时或取消"
	}
	return boundedText(redactMCP(d, err.Error()), 800)
}

type mcpHTTPTransport struct {
	base    *http.Transport
	origin  *url.URL
	headers Doc
}

func (t *mcpHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// The SSE endpoint returned by a server is untrusted too. Never forward
	// credentials (or follow redirects) to a different scheme/host/port.
	if !strings.EqualFold(r.URL.Scheme, t.origin.Scheme) || !strings.EqualFold(r.URL.Host, t.origin.Host) {
		return nil, fmt.Errorf("MCP 不允许跨来源端点或重定向")
	}
	r = r.Clone(r.Context())
	for k, v := range t.headers {
		r.Header.Set(k, str(v))
	}
	resp, err := t.base.RoundTrip(r)
	if err == nil && !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		// SDK SSE frames are capped separately. Bound JSON/error bodies before
		// the SDK reads them into memory as well.
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(resp.Body, 2<<20), resp.Body}
	}
	return resp, err
}

type mcpConnection struct {
	session   *mcp.ClientSession
	cancel    context.CancelFunc
	transport *http.Transport
}

func (c *mcpConnection) Close() {
	c.cancel()
	_ = c.session.Close()
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

func connectMCP(parent context.Context, d Doc) (*mcpConnection, error) {
	ctx, cancel := context.WithCancel(parent)
	// Initialization is bounded; a successful SSE connection must outlive
	// this timeout, so use a stopped timer rather than a per-call context.
	timer := time.AfterFunc(mcpTimeout(d), cancel)
	defer timer.Stop()
	var transport mcp.Transport
	var httpTransport *http.Transport
	if d["transport"] == "stdio" {
		args := []string{}
		for _, arg := range array(d["args"]) {
			args = append(args, str(arg))
		}
		cmd := osexec.CommandContext(ctx, str(d["command"]), args...)
		configureMCPProcess(cmd)
		cmd.WaitDelay = time.Second
		// No shell interpolation and no model/API credentials inherited from
		// the AgentMirror process. Users may explicitly add needed env keys.
		env := Doc{}
		for _, key := range []string{"PATH", "HOME", "USERPROFILE", "TMPDIR", "TEMP", "SystemRoot", "LANG"} {
			if value, ok := os.LookupEnv(key); ok {
				env[key] = value
			}
		}
		for k, v := range object(d["env"]) {
			env[k] = v
		}
		cmd.Env = []string{}
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+str(v))
		}
		cmd.Stderr = io.Discard
		transport = &mcp.CommandTransport{Command: cmd, TerminateDuration: 250 * time.Millisecond}
	} else {
		u, err := url.Parse(str(d["url"]))
		if err != nil {
			cancel()
			return nil, err
		}
		httpTransport = http.DefaultTransport.(*http.Transport).Clone()
		httpTransport.Proxy = nil
		httpTransport.ResponseHeaderTimeout = mcpTimeout(d)
		httpTransport.TLSHandshakeTimeout = min(mcpTimeout(d), 10*time.Second)
		client := &http.Client{Transport: &mcpHTTPTransport{httpTransport, u, object(d["headers"])}, CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("MCP 不允许重定向，请配置最终地址")
		}}
		if d["transport"] == "sse" {
			transport = &mcp.SSEClientTransport{Endpoint: u.String(), HTTPClient: client, MaxEventSize: 2 << 20}
		} else {
			transport = &mcp.StreamableClientTransport{Endpoint: u.String(), HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: 2 << 20}
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "AgentMirror", Version: "4.8.0"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	// This interoperable version avoids a speculative modern handshake on
	// older servers. The SDK negotiates older supported versions normally.
	session, err := client.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		cancel()
		if session != nil {
			_ = session.Close()
		}
		if httpTransport != nil {
			httpTransport.CloseIdleConnections()
		}
		return nil, err
	}
	return &mcpConnection{session, cancel, httpTransport}, nil
}

func discoverMCP(ctx context.Context, conn *mcpConnection) ([]any, error) {
	tools := []any{}
	seen, cursors := map[string]bool{}, map[string]bool{}
	cursor := ""
	for {
		page, err := conn.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, t := range page.Tools {
			if t.Name == "" || len(t.Name) > 256 || seen[t.Name] {
				return nil, fmt.Errorf("MCP 返回无效或重复工具名")
			}
			seen[t.Name] = true
			schema, schemaErr := parseAgentJSON(string(jsonBytes(t.InputSchema)))
			if schemaErr != nil {
				return nil, fmt.Errorf("MCP 工具 %s 的参数定义无效", t.Name)
			}
			if schema["type"] != "object" || len(jsonBytes(schema)) > 32000 {
				return nil, fmt.Errorf("MCP 工具 %s 的参数必须为对象，且不超过 32 KB", t.Name)
			}
			tools = append(tools, Doc{"name": t.Name, "description": boundedText(t.Description, 4000), "parameters": schema})
			if len(tools) > 128 || len(jsonBytes(tools)) > 384000 {
				return nil, fmt.Errorf("MCP 工具目录过大（最多 128 个工具 / 384 KB）")
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			return tools, nil
		}
		if cursors[cursor] || len(cursors) >= 128 {
			return nil, fmt.Errorf("MCP 工具目录分页无效")
		}
		cursors[cursor] = true
	}
}

func mcpToolName(id, name string) string {
	var readable strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			readable.WriteRune(c)
		} else {
			readable.WriteByte('_')
		}
	}
	h := sha256.Sum256([]byte(name))
	return fmt.Sprintf("mcp__%s__%.24s_%x", id, readable.String(), h[:4])
}

type mcpBoundServer struct {
	config     Doc
	connection *mcpConnection
}
type mcpBoundTool struct {
	server *mcpBoundServer
	name   string
}
type agentMCP struct {
	tools   []Doc
	calls   map[string]mcpBoundTool
	servers []*mcpBoundServer
	ctx     context.Context
}

// Called before a job is started; never persisted in the job document.
func (s *Store) mcpSnapshots(config Doc) []Doc {
	result := []Doc{}
	s.write(func(q queryer) {
		for _, id := range array(config["mcp_servers"]) {
			for _, row := range rows(q, "SELECT doc FROM mcp_servers WHERE id=?", str(id)) {
				d := decode(str(row["doc"]))
				if boolean(d["enabled"]) {
					result = append(result, d)
				}
			}
		}
	})
	return result
}

func loadAgentMCP(ctx context.Context, snapshots []Doc, event func(string)) *agentMCP {
	runtime := &agentMCP{calls: map[string]mcpBoundTool{}, ctx: ctx}
	for _, config := range snapshots {
		server := &mcpBoundServer{config: config}
		tools := array(config["tools"])
		if config["status"] != "ready" {
			event("正在连接 MCP：" + str(config["name"]))
			var err error
			server.connection, err = connectMCP(ctx, config)
			if err == nil {
				callCtx, cancel := context.WithTimeout(ctx, mcpTimeout(config))
				tools, err = discoverMCP(callCtx, server.connection)
				cancel()
			}
			if err != nil {
				if server.connection != nil {
					server.connection.Close()
				}
				event("MCP " + str(config["name"]) + " 加载失败：" + safeMCPError(config, err))
				continue
			}
		}
		if len(runtime.tools)+len(tools) > 96 || len(jsonBytes(runtime.tools))+len(jsonBytes(tools)) > 384000 {
			if server.connection != nil {
				server.connection.Close()
			}
			event("MCP " + str(config["name"]) + " 未加载：此 Agent 的 MCP 工具总量超过 96 个或 384 KB，请减少绑定服务")
			continue
		}
		runtime.servers = append(runtime.servers, server)
		for _, raw := range tools {
			t := object(raw)
			alias := mcpToolName(str(config["id"]), str(t["name"]))
			runtime.calls[alias] = mcpBoundTool{server, str(t["name"])}
			runtime.tools = append(runtime.tools, Doc{"type": "function", "function": Doc{"name": alias, "description": "[MCP · " + str(config["name"]) + "] " + str(t["description"]), "parameters": t["parameters"]}})
		}
		event(fmt.Sprintf("MCP %s 已加载 %d 个工具", config["name"], len(tools)))
	}
	return runtime
}

func (r *agentMCP) Close() {
	if r != nil {
		for _, s := range r.servers {
			if s.connection != nil {
				s.connection.Close()
			}
		}
	}
}
func (r *agentMCP) schemas() []Doc {
	if r == nil {
		return nil
	}
	return r.tools
}
func (r *agentMCP) call(ctx context.Context, name string, args Doc) (Doc, error) {
	if r == nil {
		return nil, fmt.Errorf("Agent 未绑定此 MCP 工具")
	}
	t, ok := r.calls[name]
	if !ok {
		return nil, fmt.Errorf("Agent 未绑定此 MCP 工具")
	}
	s := t.server
	if s.connection == nil {
		conn, err := connectMCP(r.ctx, s.config)
		if err != nil {
			return nil, fmt.Errorf("MCP 连接失败：%s", safeMCPError(s.config, err))
		}
		s.connection = conn
	}
	callCtx, cancel := context.WithTimeout(ctx, mcpTimeout(s.config))
	defer cancel()
	result, err := s.connection.session.CallTool(callCtx, &mcp.CallToolParams{Name: t.name, Arguments: args})
	if err != nil {
		// No automatic replay of a potentially mutating remote tool.
		return nil, fmt.Errorf("MCP 工具调用失败：%s", safeMCPError(s.config, err))
	}
	out := decode(string(jsonBytes(result)))
	delete(out, "_meta")
	// Chat currently consumes text, structured results and resource metadata.
	// Avoid sending binary base64 payloads into the text model context.
	for _, c := range array(out["content"]) {
		d := object(c)
		if _, ok := d["data"]; ok {
			delete(d, "data")
			d["notice"] = "二进制内容未载入文本上下文"
		}
		if resource := object(d["resource"]); resource != nil {
			if _, ok := resource["blob"]; ok {
				delete(resource, "blob")
				resource["notice"] = "二进制资源未载入文本上下文"
			}
		}
	}
	out = object(redactMCPValue(s.config, out))
	raw := dump(out)
	if len(raw) > 32000 {
		out = Doc{"isError": result.IsError, "truncated": true, "content": boundedText(raw, 30000), "notice": "MCP 结果过长，请用更小范围或分页参数重新读取"}
	} else {
		out = decode(raw)
	}
	if result.IsError {
		return nil, fmt.Errorf("MCP 工具返回错误：%s", boundedText(dump(out), 1600))
	}
	return out, nil
}

func redactMCPValue(config Doc, value any) any {
	switch v := value.(type) {
	case string:
		return redactMCP(config, v)
	case map[string]any:
		for k, child := range v {
			v[k] = redactMCPValue(config, child)
		}
	case []any:
		for i, child := range v {
			v[i] = redactMCPValue(config, child)
		}
	}
	return value
}
