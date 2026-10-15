package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Responses 流可能以 response.completed / response.incomplete（截断）/ response.failed 结束，
// 且没有 [DONE]。三者都必须被视为正常收尾，并从结束事件中取到 usage 用于计费。
func TestResponsesTerminalEvents(t *testing.T) {
	mkStream := func(terminal string) string {
		return strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"r1","status":"in_progress"}}`,
			``,
			`data: {"type":"response.output_text.delta","delta":"你"}`,
			``,
			`data: {"type":"` + terminal + `","response":{"id":"r1","status":"done",` +
				`"usage":{"input_tokens":60,"output_tokens":40,"total_tokens":100,` +
				`"input_tokens_details":{"cached_tokens":10}}}}`,
			``,
		}, "\n")
	}

	for _, terminal := range []string{"response.completed", "response.incomplete", "response.failed"} {
		t.Run(terminal, func(t *testing.T) {
			sse := mkStream(terminal)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(sse))
			}))
			defer upstream.Close()

			resp, err := http.Get(upstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			usage, note := relayStream(rec, resp, relayOptions{endpoint: EndpointResponses})

			if note != "" {
				t.Fatalf("%s 应视为正常收尾，却报: %q", terminal, note)
			}
			if rec.Body.String() != sse {
				t.Fatalf("流应字节级透传\nwant: %q\ngot:  %q", sse, rec.Body.String())
			}
			if usage == nil || !usage.Reported {
				t.Fatalf("%s 的 usage 必须被解析（否则漏计费）: %+v", terminal, usage)
			}
			if usage.PromptTokens != 60 || usage.CompletionTokens != 40 || usage.TotalTokens != 100 || usage.CachedTokens != 10 {
				t.Fatalf("%s usage 解析错误: %+v", terminal, usage)
			}
		})
	}

	// 缺少任何结束事件时仍要给出诊断
	t.Run("no_terminal_event", func(t *testing.T) {
		sse := strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"r1"}}`,
			``,
		}, "\n")
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(sse))
		}))
		defer upstream.Close()
		resp, _ := http.Get(upstream.URL)
		rec := httptest.NewRecorder()
		_, note := relayStream(rec, resp, relayOptions{endpoint: EndpointResponses})
		if !strings.Contains(note, "response.completed") {
			t.Fatalf("缺少结束事件应给出诊断，got %q", note)
		}
	})
}

// 包装格式（{"data": {...}}）的 usage 解析。
func TestParseUsageFromWrappedEnvelope(t *testing.T) {
	wrapped := `{"data":{"choices":[{"message":{"role":"assistant","content":"hi"}}],
		"usage":{"prompt_tokens":42,"completion_tokens":17,"total_tokens":59,
		"prompt_tokens_details":{"cached_tokens":8}}},"success":true}`
	u := ParseUsageFromJSON([]byte(wrapped), EndpointChat)
	if u == nil || !u.Reported {
		t.Fatal("wrapped envelope usage not parsed")
	}
	if u.PromptTokens != 42 || u.CompletionTokens != 17 || u.TotalTokens != 59 || u.CachedTokens != 8 {
		t.Fatalf("wrapped usage wrong: %+v", u)
	}
	// 普通格式不受影响
	plain := `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`
	if u2 := ParseUsageFromJSON([]byte(plain), EndpointChat); u2 == nil || u2.PromptTokens != 5 {
		t.Fatalf("plain format broken: %+v", u2)
	}
}
