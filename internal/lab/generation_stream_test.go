package lab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func streamEvent(w io.Writer, value any) { fmt.Fprintf(w, "data: %s\n\n", dump(value)) }
func streamDelta(delta Doc, finish any) Doc {
	return Doc{"choices": []any{Doc{"index": 0, "delta": delta, "finish_reason": finish}}}
}

func TestGenerationStreamingFirstContentBeforeCompletion(t *testing.T) {
	gate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !boolean(agentRequestBody(r)["stream"]) {
			t.Error("streaming not requested")
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		streamEvent(w, streamDelta(Doc{"reasoning_content": "先检查现有配置。"}, nil))
		w.(http.Flusher).Flush()
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		streamEvent(w, streamDelta(Doc{"content": "检查完成。"}, "stop"))
		streamEvent(w, Doc{"choices": []any{}, "usage": Doc{"completion_tokens": 17}})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	started := time.Now()
	progress := make(chan Doc, 5)
	complete := make(chan Doc, 1)
	go func() {
		message, err := generationChatWithProgress(context.Background(), Doc{"base_url": server.URL, "model": "synthetic"}, nil, nil, 1000, 2*time.Second, func(value Doc) { progress <- value })
		if err != nil {
			t.Error(err)
		}
		complete <- message
	}()
	select {
	case value := <-progress:
		if value["reasoning"] != "先检查现有配置。" {
			t.Error("missing live reasoning")
		}
	case <-time.After(time.Second):
		close(gate)
		t.Fatal("first delta blocked behind whole response")
	}
	select {
	case <-complete:
		t.Error("finished before upstream completion")
	default:
	}
	time.Sleep(150 * time.Millisecond)
	close(gate)
	message := <-complete
	meta := object(message["_response_meta"])
	if message["content"] != "检查完成。" || integer(meta["completion_tokens"]) != 17 || !boolean(meta["streamed"]) || integer(meta["elapsed_ms"]) < integer(meta["first_content_ms"])+100 {
		t.Fatalf("bad complete response: %v", message)
	}
	t.Logf("first_content=%dms completion=%dms wall=%s", integer(meta["first_content_ms"]), integer(meta["elapsed_ms"]), time.Since(started))
}

func TestGenerationStreamingAnthropicSignedBlocksAndToolFragments(t *testing.T) {
	var stream strings.Builder
	for _, event := range []Doc{
		{"type": "message_start", "message": Doc{"usage": Doc{"input_tokens": 30}}},
		{"type": "content_block_start", "index": 0, "content_block": Doc{"type": "thinking", "thinking": "", "signature": ""}},
		{"type": "content_block_delta", "index": 0, "delta": Doc{"type": "thinking_delta", "thinking": "先读取"}},
		{"type": "content_block_delta", "index": 0, "delta": Doc{"type": "signature_delta", "signature": "PRIVATE_SIGNATURE"}},
		{"type": "content_block_stop", "index": 0},
		{"type": "content_block_start", "index": 1, "content_block": Doc{"type": "redacted_thinking", "data": "PRIVATE_ENCRYPTED"}},
		{"type": "content_block_stop", "index": 1},
		{"type": "content_block_start", "index": 2, "content_block": Doc{"type": "tool_use", "id": "call1", "name": "read_module", "input": Doc{}}},
		{"type": "content_block_delta", "index": 2, "delta": Doc{"type": "input_json_delta", "partial_json": "{\"module\":"}},
		{"type": "content_block_delta", "index": 2, "delta": Doc{"type": "input_json_delta", "partial_json": "\"site\"}"}},
		{"type": "content_block_stop", "index": 2},
		{"type": "message_delta", "delta": Doc{"stop_reason": "tool_use"}, "usage": Doc{"output_tokens": 50}},
		{"type": "message_stop"},
	} {
		streamEvent(&stream, event)
	}
	var live string
	message, err := generationStreamResponse("anthropic", strings.NewReader(stream.String()), time.Now(), func(v Doc) { live += dump(v) })
	if err != nil {
		t.Fatal(err)
	}
	call := object(array(message["tool_calls"])[0])
	if object(call["function"])["arguments"] != `{"module":"site"}` || message["_display_reasoning"] != "先读取" || integer(object(message["_response_meta"])["input_tokens"]) != 30 {
		t.Fatal("incorrect normalized response", message)
	}
	_, body, _, err := generationProviderRequest(Doc{"base_url": "http://example.com", "protocol": "anthropic"}, []Doc{message}, nil, 1000)
	if err != nil || !strings.Contains(dump(body), "PRIVATE_SIGNATURE") || !strings.Contains(dump(body), "PRIVATE_ENCRYPTED") {
		t.Fatal("signed replay lost")
	}
	if strings.Contains(live, "PRIVATE") {
		t.Fatal("opaque provider content exposed in preview")
	}
}

func TestGenerationStreamingOpenAIParallelToolsAndSSEFrames(t *testing.T) {
	var stream strings.Builder
	stream.WriteString(": heartbeat\r\n\r\nevent: message\r\ndata: {\r\ndata: \"choices\": []}\r\n\r\n")
	for _, delta := range []Doc{
		{"tool_calls": []any{Doc{"index": 0, "id": "callA", "function": Doc{"name": "read_file", "arguments": "{\"path\":"}}, Doc{"index": 1, "id": "callB", "function": Doc{"name": "list_files", "arguments": "{"}}}},
		{"tool_calls": []any{Doc{"index": 1, "function": Doc{"arguments": "}"}}, Doc{"index": 0, "function": Doc{"arguments": "\"首页.html\"}"}}}},
	} {
		streamEvent(&stream, streamDelta(delta, nil))
	}
	streamEvent(&stream, streamDelta(Doc{}, "tool_calls"))
	stream.WriteString("data: [DONE]\n")
	message, err := generationStreamResponse("openai", strings.NewReader(stream.String()), time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := array(message["tool_calls"])
	if len(calls) != 2 || object(object(calls[0])["function"])["arguments"] != `{"path":"首页.html"}` || object(calls[1])["id"] != "callB" {
		t.Fatal("mixed tool fragments", message)
	}
}

func TestGenerationStreamingFailuresNeverReturnPartialMessage(t *testing.T) {
	for _, tc := range []struct{ name, protocol, body, code string }{
		{"cutoff", "openai", "data: {\"choices\":[{\"delta\":{\"content\":\"unfinished\"}}]}\n\n", "stream_interrupted"},
		{"missing_finish", "openai", "data: [DONE]\n\n", "stream_interrupted"},
		{"invalid", "openai", "data: PRIVATE_BROKEN\n\n", "invalid_stream"},
		{"upstream_error", "anthropic", "data: {\"type\":\"error\",\"error\":{\"message\":\"PRIVATE_ERROR\"}}\n\n", "upstream_unavailable"},
		{"truncated", "openai", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", "truncated"},
		{"unclosed", "anthropic", "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"x\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", "stream_interrupted"},
		{"limit", "openai", "data: " + strings.Repeat(" ", maxGenerationJSON) + "\n\n", "response_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message, err := generationStreamResponse(tc.protocol, strings.NewReader(tc.body), time.Now(), nil)
			var typed *generationResponseError
			if message != nil || !errors.As(err, &typed) || typed.code != tc.code || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestGenerationStreamingToolsWaitForCompletionAndLiveIsTransient(t *testing.T) {
	a := generationTestApp(t)
	gate, sent := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		_ = agentRequestBody(r)
		if requests.Add(1) > 1 {
			generationEnvelope(w, "完成")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		streamEvent(w, streamDelta(Doc{"tool_calls": []any{Doc{"index": 0, "id": "call1", "function": Doc{"name": "list_files", "arguments": "{}"}}}}, nil))
		w.(http.Flusher).Flush()
		close(sent)
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		streamEvent(w, streamDelta(Doc{}, "tool_calls"))
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "列出文件"}, 202)
	id := str(job["id"])
	<-sent
	var progress Doc
	for i := 0; i < 100; i++ {
		progress = generationRequest(t, a, "GET", "jobs/"+id+"?progress=1", nil, 200)
		if object(progress["live_response"]) != nil {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if object(progress["live_response"]) == nil {
		close(gate)
		t.Fatal("progress missing")
	}
	for _, v := range array(progress["events"]) {
		if object(v)["stage"] == "tool_call" {
			t.Error("tool ran before stream ended")
		}
	}
	if a.generation.job(id)["live_response"] != nil {
		t.Error("partial output persisted")
	}
	close(gate)
	done := generationWait(t, a, id)
	calls := 0
	for _, v := range array(done["events"]) {
		if object(v)["stage"] == "tool_call" {
			calls++
		}
	}
	if done["status"] != "completed" || calls != 1 || done["live_response"] != nil {
		t.Fatal("incorrect completion", done)
	}
	a.generation.mu.Lock()
	defer a.generation.mu.Unlock()
	if len(a.generation.live) != 0 {
		t.Fatal("preview leaked after completion")
	}
}

func TestGenerationStreamingCancellationAndConnectionReuse(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = agentRequestBody(r)
		generationEnvelope(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	for i := 0; i < 2; i++ {
		if _, err := generationChat(context.Background(), Doc{"base_url": server.URL, "model": "test"}, nil, nil, 100); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatal("connections were not reused")
	}
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		streamEvent(w, streamDelta(Doc{"content": "partial"}, nil))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer blocked.Close()
	ctx, cancel := context.WithCancel(context.Background())
	_, err := generationChatWithProgress(ctx, Doc{"base_url": blocked.URL, "model": "test"}, nil, nil, 100, time.Second, func(Doc) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	_, err = generationChatWithTimeout(context.Background(), Doc{"base_url": blocked.URL, "model": "test"}, nil, nil, 100, 30*time.Millisecond)
	var typed *generationResponseError
	if !errors.As(err, &typed) || typed.code != "request_timeout" {
		t.Fatal("stream deadline not applied", err)
	}
}

func TestGenerationStreamingSettingPersistsAndDisablesWireStream(t *testing.T) {
	a := generationTestApp(t)
	p := generationRequest(t, a, "POST", "providers", Doc{"name": "stream", "base_url": "http://example.com", "model": "test"}, 200)
	if p["streaming"] != true {
		t.Fatal("not default enabled")
	}
	id := str(p["id"])
	generationRequest(t, a, "POST", "providers", Doc{"id": id, "streaming": false}, 200)
	generationRequest(t, a, "POST", "providers", Doc{"id": id, "name": "renamed"}, 200)
	generationRequest(t, a, "POST", "providers", Doc{"id": id, "streaming": "false"}, 400)
	initGenerationProviders(a.store)
	settings := generationProvider(a.store.db, id, true)
	_, body, _, err := generationProviderRequest(settings, nil, nil, 100)
	if err != nil || body["stream"] != false {
		t.Fatal("disabled streaming not preserved")
	}
	generationRequest(t, a, "DELETE", "providers/"+id, nil, 200)
	if count(a.store.db, "SELECT count(*) FROM generation_provider_streaming WHERE provider_id=?", id) != 0 {
		t.Fatal("orphan options")
	}
}
