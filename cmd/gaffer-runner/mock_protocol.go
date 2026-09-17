package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"github.com/korallis/letmecook/internal/inference"
	"io"
	"net/http"
	"slices"
	"sync/atomic"
	"time"
)

// The authenticated synthetic mock uses the same closed provider envelope as the
// inference boundary. Arbitrary tool schema/data stays data; model/stream/route
// remain interpreted and pinned. Only accepted requests consume hang-after.
func mockGatewayHandler(ctx context.Context, secret string, models []string, hangAfter *uint64) http.Handler {
	var requests atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		// The Messages boundary uses the protocol's x-api-key upstream form.
		// Bearer remains supported; a presented wrong bearer cannot fall back.
		if auth == "" && r.URL.Path == "/v1/messages" {
			auth = "Bearer " + r.Header.Get("x-api-key")
		}
		if subtle.ConstantTimeCompare([]byte(auth), []byte("Bearer "+secret)) != 1 {
			http.Error(w, "denied", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/v1/models" {
			data := []any{}
			for _, model := range models {
				data = append(data, map[string]string{"id": model, "object": "model", "owned_by": "test-only"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
			return
		}
		protocol := map[string]string{"/v1/chat/completions": "chat_completions", "/v1/responses": "responses", "/v1/messages": "messages"}[r.URL.Path]
		if r.Method != "POST" || protocol == "" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil || len(body) > 65536 {
			http.Error(w, "malformed", 400)
			return
		}
		model, err := inference.RequestModel(body, protocol)
		if err != nil {
			http.Error(w, "malformed", 400)
			return
		}
		if !slices.Contains(models, model) {
			http.Error(w, "model_refused", 403)
			return
		}
		var envelope struct {
			Stream bool `json:"stream"`
		}
		if json.Unmarshal(body, &envelope) != nil {
			http.Error(w, "malformed", 400)
			return
		}
		accepted := requests.Add(1)
		if hangAfter != nil && accepted > *hangAfter {
			// Neither deadline setup failure nor shutdown may synthesize an empty 200.
			if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
				panic(http.ErrAbortHandler)
			}
			<-ctx.Done()
			panic(http.ErrAbortHandler)
		}
		if envelope.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, mockStreamingResponse(protocol, model))
			return
		}
		_ = json.NewEncoder(w).Encode(mockJSONResponse(protocol, model))
	})
}

func mockJSONResponse(protocol, model string) any {
	switch protocol {
	case "chat_completions":
		return map[string]any{"id": "test-only", "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "synthetic mock response"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1}}
	case "messages":
		return map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "model": model, "content": []any{map[string]string{"type": "text", "text": "ok"}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}}
	default:
		return map[string]any{"id": "resp_test", "object": "response", "created_at": 1, "model": model, "status": "completed", "output": []any{map[string]any{"id": "msg_test", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "ok", "annotations": []any{}}}}}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
	}
}

// Synthetic SSE fixtures match the pinned adapter's hostile-config probe server.
func mockStreamingResponse(protocol, model string) string {
	emit := func(kind string, value any) string {
		raw, _ := json.Marshal(value)
		return "event: " + kind + "\ndata: " + string(raw) + "\n\n"
	}
	switch protocol {
	case "chat_completions":
		raw, _ := json.Marshal(map[string]any{"id": "probe", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1}})
		return "data: " + string(raw) + "\n\ndata: [DONE]\n\n"
	case "messages":
		return emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}}) +
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}}) +
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "ok"}}) +
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}) +
			emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}}) + emit("message_stop", map[string]string{"type": "message_stop"})
	case "responses":
		part := map[string]any{"type": "output_text", "text": "ok", "annotations": []any{}, "logprobs": []any{}}
		item := map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
		response := map[string]any{"id": "resp_probe", "object": "response", "created_at": 1, "model": model, "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "input_tokens_details": map[string]int{"cached_tokens": 0}, "output_tokens_details": map[string]int{"reasoning_tokens": 0}}}
		return emit("response.created", map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": "resp_probe", "object": "response", "created_at": 1, "model": model, "status": "in_progress", "output": []any{}}}) +
			emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "sequence_number": 1, "output_index": 0, "item": map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}) +
			emit("response.content_part.added", map[string]any{"type": "response.content_part.added", "sequence_number": 2, "output_index": 0, "item_id": "msg_probe", "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}}) +
			emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "sequence_number": 3, "output_index": 0, "item_id": "msg_probe", "content_index": 0, "delta": "ok", "logprobs": []any{}}) +
			emit("response.output_text.done", map[string]any{"type": "response.output_text.done", "sequence_number": 4, "output_index": 0, "item_id": "msg_probe", "content_index": 0, "text": "ok", "logprobs": []any{}}) +
			emit("response.content_part.done", map[string]any{"type": "response.content_part.done", "sequence_number": 5, "output_index": 0, "item_id": "msg_probe", "content_index": 0, "part": part}) +
			emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "sequence_number": 6, "output_index": 0, "item": item}) +
			emit("response.completed", map[string]any{"type": "response.completed", "sequence_number": 7, "response": response})
	}
	return ""
}
