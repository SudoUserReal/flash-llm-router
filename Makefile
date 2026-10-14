.PHONY: build web run test test-unit test-integration docker docker-down dev-web dev-api clean

BINARY := flr

## 完整构建：前端 + 后端（前端产物通过 go:embed 打进二进制）
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) .

## 仅构建前端
web:
	cd web && npm install --no-audit --no-fund && npm run build

## 运行（需要 DATABASE_URL 指向可用 PostgreSQL）
run: build
	./$(BINARY)

## 全部测试（需要本地或远程 PostgreSQL；TEST_DATABASE_URL 可覆盖）
test:
	go test ./...

## 仅单元测试（不需要数据库）
test-unit:
	go test ./internal/auth/ ./internal/gateway/

## 集成测试（需要 PostgreSQL，PG 不可用时自动跳过）
test-integration:
	go test ./internal/httpapi/ -v

## Docker 一键启动（app + postgres）
docker:
	docker compose up --build -d

docker-down:
	docker compose down

## 前端开发服务器（代理到本地 8080 后端）
dev-web:
	cd web && npm run dev

## 生成验证码样例图（人工检查观感）
captcha-preview:
	CAPTCHA_DUMP=/tmp/captcha_sample.png go test ./internal/auth/ -run TestCaptchaPreview -v

clean:
	rm -f $(BINARY)
