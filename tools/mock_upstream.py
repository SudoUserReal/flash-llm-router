#!/usr/bin/env python3
"""开发用 mock 上游：模拟 OpenAI 兼容 API（chat/completions + models + responses）。"""
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def _json(self, code, obj):
        data = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path.startswith('/v1/models'):
            self._json(200, {'object': 'list', 'data': [{'id': 'mock-model', 'object': 'model'}]})
        else:
            self._json(404, {'error': {'message': 'not found'}})

    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0))
        raw = self.rfile.read(n)
        try:
            req = json.loads(raw or b'{}')
        except Exception:
            req = {}

        if self.path.startswith('/v1/chat/completions'):
            if req.get('stream'):
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.end_headers()
                for piece in ['你好', '，我是', '模拟上游']:
                    chunk = {'id': 'chatcmpl-mock', 'object': 'chat.completion.chunk',
                             'choices': [{'index': 0, 'delta': {'content': piece}}]}
                    self.wfile.write(f"data: {json.dumps(chunk, ensure_ascii=False)}\n\n".encode())
                    self.wfile.flush()
                    time.sleep(0.15)
                usage = {'id': 'chatcmpl-mock', 'object': 'chat.completion.chunk', 'choices': [],
                         'usage': {'prompt_tokens': 99, 'completion_tokens': 33, 'total_tokens': 132,
                                   'prompt_tokens_details': {'cached_tokens': 11}}}
                self.wfile.write(f"data: {json.dumps(usage)}\n\n".encode())
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()
                return
            self._json(200, {
                'id': 'chatcmpl-mock', 'object': 'chat.completion', 'model': req.get('model', 'mock-model'),
                'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': '你好，我是模拟上游的回复。'},
                             'finish_reason': 'stop'}],
                'usage': {'prompt_tokens': 123, 'completion_tokens': 45, 'total_tokens': 168,
                          'prompt_tokens_details': {'cached_tokens': 23}},
            })
        elif self.path.startswith('/v1/responses'):
            self._json(200, {
                'id': 'resp_mock', 'object': 'response', 'status': 'completed',
                'output': [{'type': 'message', 'role': 'assistant',
                            'content': [{'type': 'output_text', 'text': '你好，我是模拟 Responses 回复。'}]}],
                'usage': {'input_tokens': 66, 'output_tokens': 22, 'total_tokens': 88,
                          'input_tokens_details': {'cached_tokens': 6}},
            })
        else:
            self._json(404, {'error': {'message': 'not found'}})

    def log_message(self, *args):
        pass


if __name__ == '__main__':
    print('mock upstream on http://127.0.0.1:9999')
    ThreadingHTTPServer(('127.0.0.1', 9999), Handler).serve_forever()
