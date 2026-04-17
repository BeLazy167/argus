FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /argus ./cmd/argus
# Embeds SQL files — no runtime volume needed.
RUN CGO_ENABLED=0 GOOS=linux go build -o /migrate ./cmd/migrate

# Install staticcheck (Go SAST tool)
RUN go install honnef.co/go/tools/cmd/staticcheck@2025.1.1

FROM alpine:3.21

# Core deps
RUN apk --no-cache add ca-certificates

# Universal ctags for symbol extraction (40+ languages, ~2MB)
RUN apk --no-cache add universal-ctags || apk --no-cache add ctags || true

# Node.js + eslint for TS/JS SAST (optional — graceful degradation if unavailable)
RUN apk --no-cache add nodejs npm && \
    npm install -g eslint@9.17.0 && \
    npm cache clean --force || true

# Python + semgrep for universal SAST (optional — largest dependency)
RUN apk --no-cache add python3 py3-pip && \
    pip3 install --break-system-packages semgrep==1.100.0 && \
    rm -rf /root/.cache || true

# Copy staticcheck from builder
COPY --from=builder /go/bin/staticcheck /usr/local/bin/staticcheck

COPY --from=builder /argus /argus
COPY --from=builder /migrate /migrate

EXPOSE 8080
ENTRYPOINT ["/argus"]
