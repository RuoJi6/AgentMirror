package lab

import (
	"fmt"
	"time"
)

// Missing options preserve the defaults of existing installations. Kept apart
// from credentials so upgrades do not rewrite existing provider records.
func generationTimeout(settings Doc, field string) time.Duration {
	fallback := generationRequestTimeout
	if field == "job_timeout_seconds" {
		fallback = generationJobTimeout
	}
	if seconds := integer(settings[field]); seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func validateGenerationTimeouts(raw, old, result Doc) {
	for _, option := range []struct {
		field, label string
		min, max     int
	}{
		{"request_timeout_seconds", "单次模型请求超时", 10, 3600},
		{"job_timeout_seconds", "整个任务超时", 30, 7200},
	} {
		value := optional(raw, option.field, int(generationTimeout(old, option.field).Seconds()))
		n, ok := number(value)
		if !ok || n < int64(option.min) || n > int64(option.max) {
			fail(400, fmt.Sprintf("%s需为 %d–%d 秒的整数", option.label, option.min, option.max))
		}
		result[option.field] = int(n)
	}
	if integer(result["job_timeout_seconds"]) < integer(result["request_timeout_seconds"]) {
		fail(400, "整个任务超时不能小于单次模型请求超时")
	}
}

// Input window and per-response output are independent provider capabilities.
func generationTokenLimit(settings Doc, field string) int {
	if n := integer(settings[field]); n > 0 {
		return n
	}
	if field == "max_output_tokens" {
		return 24000
	}
	return 128000
}
func validateGenerationTokenLimits(raw, old, result Doc) {
	for _, option := range []struct {
		field, label string
		min, max     int
	}{
		{"context_window_tokens", "上下文窗口", 16000, 1048576},
		{"max_output_tokens", "单次输出上限", 512, 131072},
	} {
		n, ok := number(optional(raw, option.field, generationTokenLimit(old, option.field)))
		if !ok || n < int64(option.min) || n > int64(option.max) {
			fail(400, fmt.Sprintf("%s需为 %d–%d token 的整数", option.label, option.min, option.max))
		}
		result[option.field] = int(n)
	}
	if integer(result["max_output_tokens"])+4096 >= integer(result["context_window_tokens"]) {
		fail(400, "上下文窗口至少需比输出上限多 4096 token")
	}
}
