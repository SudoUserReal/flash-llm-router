package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type relayOptions struct {
	endpoint    string
	filterUsage bool // 过滤我们注入 include_usage 所产生的"仅含 usage"事件，保持客户端原语义
}

// relayStream 逐事件转发上游 SSE 流式响应，同时解析 usage 用于计费。
// 返回解析到的 usage 与流中断错误（读上游/写客户端）。
func relayStream(w http.ResponseWriter, resp *http.Response, opts relayOptions) (*Usage, error) {
	copyResponseHeaders(w.Header(), resp.Header)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	flush()

	br := bufio.NewReaderSize(resp.Body, 1<<20)

	var (
		pending  []byte   // 当前事件的原始字节（含行尾）
		payloads [][]byte // 当前事件中的 data 行内容
		usage    *Usage
		writeErr error
		readErr  error
	)

	flushEvent := func() {
		if len(pending) == 0 {
			return
		}
		drop := opts.filterUsage && isPureUsageEvent(payloads, opts.endpoint)
		if !drop && writeErr == nil {
			if _, err := w.Write(pending); err != nil {
				writeErr = fmt.Errorf("client write: %w", err)
			}
			flush()
		}
		pending = pending[:0]
		payloads = payloads[:0]
	}

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if isBlankSSELine(line) {
				flushEvent()
				if writeErr != nil {
					break
				}
			} else {
				pending = append(pending, line...)
				if p := sseDataPayload(line); p != nil {
					payloads = append(payloads, p)
					parseUsageFromPayload(p, opts.endpoint, &usage)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = fmt.Errorf("upstream stream: %w", err)
			}
			if len(pending) > 0 {
				flushEvent()
			}
			break
		}
	}
	flush()

	if writeErr != nil {
		return usage, writeErr
	}
	return usage, readErr
}

func isBlankSSELine(line []byte) bool {
	trimmed := bytes.TrimRight(line, "\r\n")
	return len(trimmed) == 0
}

// sseDataPayload 提取 "data:" 行的内容。
func sseDataPayload(line []byte) []byte {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil
	}
	payload := bytes.TrimRight(line[len("data:"):], "\r\n")
	payload = bytes.TrimLeft(payload, " ")
	if len(payload) == 0 {
		return nil
	}
	return payload
}

// isPureUsageEvent 判断事件是否为"仅含 usage、无 content"的计费事件（choices 为空）。
func isPureUsageEvent(payloads [][]byte, endpoint string) bool {
	if endpoint == EndpointResponses {
		return false // responses 流不使用注入，不视为纯 usage
	}
	found := false
	for _, p := range payloads {
		if bytes.Equal(bytes.TrimSpace(p), []byte("[DONE]")) {
			continue
		}
		var chunk struct {
			Choices []json.RawMessage `json:"choices"`
			Usage   json.RawMessage   `json:"usage"`
		}
		if err := json.Unmarshal(p, &chunk); err != nil {
			return false
		}
		hasUsage := len(chunk.Usage) > 0 && !bytes.Equal(bytes.TrimSpace(chunk.Usage), []byte("null"))
		if !hasUsage || len(chunk.Choices) > 0 {
			return false
		}
		found = true
	}
	return found
}
