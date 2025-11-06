FROM golang:1.24-bookworm AS builder

# Build arguments
ARG ENABLE_COVERAGE=false
ARG COVERAGE_SERVER_URL=https://raw.githubusercontent.com/psturc/go-coverage-http/main/server/coverage_server.go

WORKDIR /app

# Copy application code
COPY example_app.go ./

# Download coverage server if building with coverage
RUN if [ "$ENABLE_COVERAGE" = "true" ]; then \
        echo "📥 Downloading coverage server from GitHub..."; \
        wget -q "$COVERAGE_SERVER_URL" -O coverage_server.go || \
        curl -sL "$COVERAGE_SERVER_URL" -o coverage_server.go; \
    fi

# Build binary with or without coverage instrumentation
RUN if [ "$ENABLE_COVERAGE" = "true" ]; then \
        echo "🧪 Building with coverage instrumentation..."; \
        CGO_ENABLED=0 go build -cover -covermode=atomic -o app example_app.go coverage_server.go; \
    else \
        echo "🚀 Building production binary..."; \
        CGO_ENABLED=0 go build -o app example_app.go; \
    fi

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/app /app/app

# Build metadata environment variables (for MetadataHandler)
ARG GIT_COMMIT_SHA=unknown
ARG GIT_REPO_URL=unknown
ENV GIT_COMMIT_SHA=${GIT_COMMIT_SHA} \
    GIT_REPO_URL=${GIT_REPO_URL}

USER 65532:65532

CMD ["/app/app"]

