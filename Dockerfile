FROM golang:1.24-bookworm AS builder

# Build arguments
ARG ENABLE_COVERAGE=false
ARG COVERAGE_SERVER_URL=https://raw.githubusercontent.com/psturc/go-coverage-http/main/server/coverage_server.go

WORKDIR /app

# Copy main application code
COPY example_app.go ./

# Conditionally download coverage server (only for test builds)
RUN if [ "$ENABLE_COVERAGE" = "true" ]; then \
        echo "📥 Downloading coverage server from: $COVERAGE_SERVER_URL"; \
        wget -q "$COVERAGE_SERVER_URL" -O coverage_server.go || \
        curl -sL "$COVERAGE_SERVER_URL" -o coverage_server.go; \
        echo "✅ Coverage server downloaded"; \
    fi

# Conditional build
RUN if [ "$ENABLE_COVERAGE" = "true" ]; then \
        echo "🧪 Building with coverage instrumentation..."; \
        CGO_ENABLED=0 go build -cover -covermode=atomic -o app example_app.go coverage_server.go; \
        echo "✅ Test build complete (with coverage)"; \
    else \
        echo "🚀 Building production binary..."; \
        CGO_ENABLED=0 go build -o app example_app.go; \
        echo "✅ Production build complete (no coverage)"; \
    fi

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Copy the binary
COPY --from=builder /app/app /app/app

# Metadata
ENV APP_NAME=coverage-http-demo
ENV APP_VERSION=1.0.0

USER 65532:65532

# Run the application
CMD ["/app/app"]

