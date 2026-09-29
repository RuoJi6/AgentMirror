package lab

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Transient empty model replies are retried at the request boundary, not by
// restarting the agent/tools. One job shares this bounded recovery budget.
const maxAgentResponseRetries = 2
const maxAgentRecoveryTokens = 24000

type generationResponseError struct {
	code       string
	retryable  bool
	detail     string
	diagnostic Doc
	reasoning  string // Provider-returned plaintext, never included in the error string.
}

func (e *generationResponseError) Error() string {
	reason := str(e.diagnostic["finish_reason"])
	if reason != "" && reason != "unspecified" {
		return e.detail + "（结束原因：" + reason + "）"
	}
	return e.detail
}

func (m *generationManager) agentChat(ctx context.Context, id string, settings Doc, messages []Doc, round int, recoveries *int, tools []Doc) (Doc, error) {
	maxTokens := generationTokenLimit(settings, "max_output_tokens")
	// Keep recovery-only instructions local; do not forge user conversation turns.
	wireMessages := messages
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.agentEvent(id, "model", fmt.Sprintf("AI 正在处理第 %d 轮", round), Doc{"round": round, "attempt": attempt, "max_tokens": maxTokens, "request_timeout_seconds": int(generationTimeout(settings, "request_timeout_seconds").Seconds())})
		message, err := generationChatWithProgress(ctx, settings, wireMessages, tools, maxTokens, generationTimeout(settings, "request_timeout_seconds"), func(value Doc) {
			value["round"], value["attempt"] = round, attempt
			m.setLiveResponse(id, value)
		})
		m.setLiveResponse(id, nil)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			m.recordModelReasoning(id, str(message["_display_reasoning"]), round, attempt)
			meta := object(message["_response_meta"])
			m.agentEvent(id, "model_response", "模型回复已接收", Doc{"round": round, "attempt": attempt, "max_tokens": maxTokens, "diagnostic": meta})
			return message, nil
		}
		var responseErr *generationResponseError
		if !errors.As(err, &responseErr) {
			m.agentEvent(id, "model_error", err.Error(), Doc{"round": round, "attempt": attempt, "diagnostic": Doc{"code": "request_error"}})
			return nil, err
		}
		m.recordModelReasoning(id, responseErr.reasoning, round, attempt)
		fields := Doc{"round": round, "attempt": attempt, "max_tokens": maxTokens, "diagnostic": responseErr.diagnostic}
		detail := responseErr.Error()
		if !responseErr.retryable || *recoveries >= maxAgentResponseRetries {
			if responseErr.retryable {
				if *recoveries >= maxAgentResponseRetries {
					detail += "；已达到本任务 2 次自动重试上限，请分步处理，或检查模型输出上限与思考预算"
				} else {
					detail += "；增加输出预算后仍未完成，请缩小本轮需求"
				}
			}
			m.agentEvent(id, "model_error", detail, fields)
			return nil, fmt.Errorf("%s", detail)
		}
		*recoveries++
		if responseErr.code == "truncated" {
			wireMessages = append(append([]Doc{}, messages...), Doc{"role": "user", "content": "The previous response was truncated at the output token limit. No tools from that response were executed. Continue the SAME requested task with a short response or ONE small complete tool call. Do not regenerate whole files or repeat completed actions. For file changes use edit_file; for new files use write_file for the first chunk (under 4000 characters) then append_file in subsequent rounds, using returned hashes. For scenarios use upsert_rule for one rule at a time. Keep reasoning brief and leave enough tokens for complete JSON arguments. If these tools are unavailable, provide a concise answer or use an available scoped tool."})
		}
		// Empty Anthropic end_turn responses need a continuation hint, rather than
		// replaying an empty assistant block. Tool results/signatures remain intact.
		if str(settings["protocol"]) == "anthropic" && (responseErr.code == "empty_response" || responseErr.code == "reasoning_only") {
			wireMessages = append(append([]Doc{}, messages...), Doc{"role": "user", "content": "Please continue the current user request from the tool results above. Reply to the user, or call only the remaining necessary tools. Do not repeat completed work or add unrelated modules."})
		}
		fields["retry"] = *recoveries
		fields["next_max_tokens"] = maxTokens
		m.agentEvent(id, "model_retry", detail+fmt.Sprintf("；正在自动重试（%d/%d）", *recoveries, maxAgentResponseRetries), fields)
		timer := time.NewTimer(time.Duration(*recoveries) * 500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// A separate bounded event preserves chronology without treating reasoning as
// the final answer or persisting signed/encrypted provider state.
func (m *generationManager) recordModelReasoning(id, text string, round, attempt int) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	const limit = 32768
	m.agentEvent(id, "reasoning", "模型思考", Doc{
		"round": round, "attempt": attempt,
		"reasoning_preview": boundedText(strings.Join(strings.Fields(text), " "), 140),
		"reasoning":         Doc{"text": boundedText(text, limit), "truncated": len([]rune(text)) > limit, "characters": len([]rune(text))},
	})
}
