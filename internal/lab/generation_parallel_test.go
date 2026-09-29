package lab

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestGenerationParallelConversationsAndCancellation(t *testing.T) {
	a := generationTestApp(t)
	entered := make(chan string, maxConcurrentGenerationJobs)
	gates := make(map[string]chan struct{})
	for i := 0; i < maxConcurrentGenerationJobs; i++ {
		gates[fmt.Sprintf("parallel-%d", i)] = make(chan struct{})
	}
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		messages := array(agentRequestBody(r)["messages"])
		prompt := str(decode(str(object(messages[len(messages)-1])["content"]))["request"])
		entered <- prompt
		select {
		case <-gates[prompt]:
			generationEnvelope(w, "答复："+prompt)
		case <-r.Context().Done():
		}
	})
	// Release the fake provider before its server cleanup even on assertion failure.
	t.Cleanup(func() {
		for _, gate := range gates {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	})
	jobs := []Doc{}
	for i := 0; i < maxConcurrentGenerationJobs; i++ {
		jobs = append(jobs, generationRequest(t, a, "POST", "jobs", Doc{"prompt": fmt.Sprintf("parallel-%d", i)}, 202))
	}
	seen := map[string]bool{}
	for range jobs {
		select {
		case prompt := <-entered:
			seen[prompt] = true
		case <-time.After(3 * time.Second):
			t.Fatal("provider requests were serialized")
		}
	}
	if len(seen) != len(jobs) {
		t.Fatal("parallel jobs shared their input")
	}
	generationRequest(t, a, "POST", "jobs", Doc{"prompt": "overflow"}, 429)
	generationRequest(t, a, "POST", "jobs", Doc{"prompt": "overlapping turn", "conversation_id": jobs[0]["conversation_id"]}, 409)
	generationRequest(t, a, "POST", "jobs/"+str(jobs[0]["id"])+"/cancel", Doc{}, 200)
	for i := len(jobs) - 1; i > 0; i-- {
		if a.generation.job(str(jobs[i]["id"]))["status"] != "running" {
			t.Fatal("cancel affected another conversation")
		}
		close(gates[fmt.Sprintf("parallel-%d", i)])
		done := generationWait(t, a, str(jobs[i]["id"]))
		if done["status"] != "completed" || object(done["result"])["summary"] != fmt.Sprintf("答复：parallel-%d", i) {
			t.Fatalf("result crossed conversations: %s", dump(done))
		}
	}
	if a.generation.job(str(jobs[0]["id"]))["status"] != "cancelled" {
		t.Fatal("cancelled job was overwritten")
	}
}

func TestGenerationSameConversationConcurrentSubmission(t *testing.T) {
	a := generationTestApp(t)
	gate := make(chan struct{})
	agentModel(t, a, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gate:
			generationEnvelope(w, "完成")
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	a.generation.saveJob(Doc{"id": "parallel-root", "conversation_id": "parallel-root", "status": "completed", "result": Doc{"kind": "message", "summary": "上下文"}})
	type outcome struct {
		job    Doc
		status int
	}
	outcomes := make(chan outcome, 12)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			defer func() {
				if p := recover(); p != nil {
					if e, ok := p.(problem); ok {
						outcomes <- outcome{status: e.status}
					} else {
						panic(p)
					}
				}
			}()
			job := a.generation.start(Doc{"prompt": "继续", "parent_job_id": "parallel-root"})
			outcomes <- outcome{job: job, status: 202}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)
	accepted := []Doc{}
	for result := range outcomes {
		if result.status == 202 {
			accepted = append(accepted, result.job)
		} else if result.status != 409 {
			t.Fatalf("unexpected status: %d", result.status)
		}
	}
	if len(accepted) != 1 {
		t.Fatalf("same conversation accepted %d concurrent turns", len(accepted))
	}
	independent := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "独立对话"}, 202)
	close(gate)
	for _, job := range append(accepted, independent) {
		if generationWait(t, a, str(job["id"]))["status"] != "completed" {
			t.Fatal("parallel completion failed")
		}
	}
	// The completed parent can continue once the current turn has ended.
	next := generationRequest(t, a, "POST", "jobs", Doc{"prompt": "下一轮", "parent_job_id": accepted[0]["id"]}, 202)
	if next["conversation_id"] != "parallel-root" {
		t.Fatal("continuation changed conversation")
	}
	generationWait(t, a, str(next["id"]))
}
