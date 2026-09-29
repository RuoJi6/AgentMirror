package lab

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenerationTransportWaitsForCompleteResponseAndClassifiesTimeout(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "body" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(80 * time.Millisecond):
				}
				generationEnvelope(w, "complete")
			}))
			defer server.Close()
			settings := Doc{"base_url": server.URL, "model": "synthetic", "api_key": "PRIVATE_TEST_KEY"}
			messages := []Doc{{"role": "user", "content": "hello"}}
			ok, err := generationChatWithTimeout(context.Background(), settings, messages, nil, 100, 500*time.Millisecond)
			if err != nil || ok["content"] != "complete" {
				t.Fatalf("slow successful response failed: %v", err)
			}
			_, err = generationChatWithTimeout(context.Background(), settings, messages, nil, 100, 20*time.Millisecond)
			var typed *generationResponseError
			if !errors.As(err, &typed) || typed.code != "request_timeout" || !typed.retryable || integer(typed.diagnostic["elapsed_ms"]) < 10 {
				t.Fatalf("timeout misclassified: %v", err)
			}
			if phase == "body" && typed.diagnostic["phase"] != "response_read" {
				t.Fatal("body timeout missing phase")
			}
			if strings.Contains(dump(typed.diagnostic)+err.Error(), "PRIVATE") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("transport diagnostic leaked request details")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = generationChatWithTimeout(ctx, settings, messages, nil, 100, time.Second)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation was converted to a retryable error")
			}
		})
	}
}

func TestGenerationHTTPRecoveryKeepsToolResults(t *testing.T) {
	for _, status := range []int{429, 503, 401} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a := generationTestApp(t)
			var count atomic.Int32
			var original string
			agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
				body := agentRequestBody(r)
				switch count.Add(1) {
				case 1:
					agentReply(w, agentToolCall("files", "list_files", Doc{}))
				case 2:
					original = dump(body)
					w.WriteHeader(status)
					io.WriteString(w, "PRIVATE_UPSTREAM_BODY")
				case 3:
					if dump(body) != original {
						t.Error("retry changed completed tool results")
					}
					generationEnvelope(w, "已恢复")
				default:
					t.Error("unexpected retry")
				}
			})
			job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "查看文件"}, 202)
			done := generationWait(t, a, str(job["id"]))
			if status == 401 {
				if count.Load() != 2 || done["status"] != "failed" || !strings.Contains(str(done["error"]), "鉴权") {
					t.Fatal("authentication failure retried or misreported")
				}
			} else if count.Load() != 3 || done["status"] != "completed" {
				t.Fatal("transient HTTP response did not recover")
			}
			tools := 0
			for _, v := range array(done["events"]) {
				if object(v)["stage"] == "tool_call" {
					tools++
				}
			}
			if tools != 1 || strings.Contains(dump(done), "PRIVATE_UPSTREAM_BODY") {
				t.Fatal("recovery repeated tools or leaked upstream body")
			}
		})
	}
}

func TestGenerationTransientHTTPRetryBudget(t *testing.T) {
	a := generationTestApp(t)
	var count atomic.Int32
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) { count.Add(1); w.WriteHeader(503) })
	job := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "测试重试预算"}, 202)
	done := generationWait(t, a, str(job["id"]))
	if count.Load() != 3 || done["status"] != "failed" || !strings.Contains(str(done["error"]), "重试上限") {
		t.Fatal("transient errors bypassed bounded recovery")
	}
}
