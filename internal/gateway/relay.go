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

// streamProgress 跟踪流的完整性标记（用于诊断日志）。
type streamProgress struct {
	sawDone      bool // chat: 收到 [DONE]
	sawFinish    bool // chat: 收到非空 finish_reason
	sawCompleted bool // responses: 收到 response.completed
}

// relayStream 逐事件转发上游 SSE 流式响应，同时解析 usage 用于计费。
// 返回解析到的 usage 与流诊断说明（错误或"流不完整"提示，正常完成时为空）。
//
// 关键：SSE 事件之间必须由空行分隔——空行是事件的一部分，
// 转发时必须原样保留，否则客户端会把多条 data 行拼成同一个事件解析。
func relayStream(w http.ResponseWriter, resp *http.Response, opts relayOptions) (*Usage, string) {
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
		pending  []byte   // 当前事件的原始字节（含行尾与结尾空行）
		payloads [][]byte // 当前事件中的 data 行内容
		usage    *Usage
		writeErr error
		readErr  error
		prog     streamProgress
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
				// 空行 = SSE 事件分隔符：并入当前事件后整体转发，保持字节级结构
				pending = append(pending, line...)
				flushEvent()
				if writeErr != nil {
					break
				}
			} else {
				pending = append(pending, line...)
				if p := sseDataPayload(line); p != nil {
					payloads = append(payloads, p)
					parseUsageFromPayload(p, opts.endpoint, &usage)
					trackProgress(p, opts.endpoint, &prog)
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

	switch {
	case writeErr != nil:
		return usage, writeErr.Error()
	case readErr != nil:
		return usage, readErr.Error()
	}
	if opts.endpoint == EndpointResponses {
		if !prog.sawCompleted {
			return usage, "upstream stream ended without response.completed"
		}
		return usage, ""
	}
	if !prog.sawDone {
		return usage, "upstream stream ended without [DONE]"
	}
	if !prog.sawFinish {
		return usage, "upstream stream ended without finish_reason"
	}
	return usage, ""
}

func trackProgress(payload []byte, endpoint string, prog *streamProgress) {
	if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		prog.sawDone = true
		return
	}
	if endpoint == EndpointResponses {
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &ev) == nil && isResponsesTerminalEvent(ev.Type) {
			// completed / incomplete（截断）/ failed 都表示流已正常收尾
			prog.sawCompleted = true
		}
		return
	}
	var chunk struct {
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &chunk) == nil {
		for _, c := range chunk.Choices {
			if c.FinishReason != nil && *c.FinishReason != "" {
				prog.sawFinish = true
			}
		}
	}
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
