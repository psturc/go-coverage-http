# go-coverage-http

Collect Go code coverage from running applications via HTTP - no volumes, no GOCOVERDIR, no deployment modifications needed.

## Why?

Traditional coverage collection with `GOCOVERDIR` requires:
- ❌ Setting `GOCOVERDIR` environment variable
- ❌ Mounting volumes in Kubernetes for coverage data
- ❌ Modifying deployment manifests to add volume mounts
- ❌ Extracting files from volumes after tests

**This solution eliminates all of that:**
- ✅ No `GOCOVERDIR` needed
- ✅ No volume mounts required
- ✅ No deployment manifest changes
- ✅ Just inject `coverage_server.go` during build
- ✅ Collect coverage via HTTP with provided client library

## How it works

1. **Build time**: Include `server/coverage_server.go` when building with `-cover` flag
2. **Runtime**: Coverage server automatically starts on port 9095
3. **Test time**: Client library collects coverage via HTTP port-forwarding
4. **Result**: Coverage reports generated automatically

## Features

- 🚀 **HTTP Coverage Server** - Automatically starts coverage endpoint in your app
- 🔌 **Client Library** - Collect coverage from Kubernetes pods with port-forwarding
- 📊 **Report Generation** - Generate text and HTML coverage reports
- 🎯 **Minimal Setup** - Just inject one file during Docker build
- 🐳 **Kubernetes-friendly** - No volumes, no manifest modifications

## Quick Start

### 1. Add Coverage Server to Your App

Download `server/coverage_server.go` and compile it with your app (test builds only):

```dockerfile
# Download coverage server
RUN wget https://raw.githubusercontent.com/psturc/go-coverage-http/main/server/coverage_server.go

# Build with coverage
RUN go build -cover -covermode=atomic -o app example_app.go coverage_server.go
```

The coverage server will automatically start on port 9095 (configurable via `COVERAGE_PORT` env var).

### 2. Collect Coverage from Tests

```go
import coverageclient "github.com/psturc/go-coverage-http/client"

// Create client
client, _ := coverageclient.NewClient("default", "./coverage-output")

// Collect from Kubernetes pod
client.CollectCoverageFromPod(ctx, "my-pod", "my-test", 9095)

// Generate reports
client.GenerateCoverageReport("my-test")
client.GenerateHTMLReport("my-test")
```

## Example

Demo application showing how to collect coverage from a running app in Kubernetes.

### Files

- `example_app.go` - Simple HTTP server with test endpoints
- `Dockerfile` - Example Dockerfile for your app (downloads server/coverage_server.go from GitHub during build)
- `Dockerfile.local` - Local development build (uses local server/coverage_server.go from the current repository)
- `k8s-deployment.yaml` - Kubernetes deployment manifest

### Quick Start

#### 1. Build the image with coverage

```bash
# Local build with instrumentation
docker build -f Dockerfile.local --build-arg ENABLE_COVERAGE=false -t localhost/coverage-http-demo:latest ..

# Local build without instrumentation (without server/coverage_server.go)
docker build -f Dockerfile --build-arg ENABLE_COVERAGE=false -t localhost/coverage-http-demo:latest .
```

#### 2. Deploy to Kubernetes

```bash
kubectl apply -f k8s-deployment.yaml
```

#### 3. Run E2E tests and collect coverage

```bash
cd ../test
go test -v
```

The tests will:
- Execute requests against the running pod
- Collect coverage data via port-forwarding
- Generate coverage reports in `./coverage-output/`

### Endpoints

- `:8080/health` - Health check
- `:8080/greet?name=X` - Greeting endpoint
- `:8080/calculate` - Calculation endpoint
- `:9095/coverage` - Coverage collection endpoint (test builds only)
- `:9095/health` - Coverage server health check