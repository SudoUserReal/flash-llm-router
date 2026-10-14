# ---------- 前端构建 ----------
FROM node:22-alpine AS webbuild
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 后端构建（嵌入前端产物）----------
FROM golang:1.25-alpine AS gobuild
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=webbuild /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/flr .

# ---------- 运行时 ----------
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=gobuild /out/flr /usr/local/bin/flr
RUN adduser -D -H flr
USER flr
EXPOSE 8080
ENTRYPOINT ["flr"]
