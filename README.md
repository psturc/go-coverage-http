[![codecov](https://codecov.io/gh/psturc/go-coverage-http/branch/main/graph/badge.svg)](https://codecov.io/gh/psturc/go-coverage-http)
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

📖 **Want technical details?** See [TECHNICAL.md](TECHNICAL.md) for in-depth architecture, algorithms, and implementation details.

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

// Discover pod dynamically using label selector
podName, _ := client.GetPodName("app=my-app")

// Or use the manual pod name
// podName := "my-pod-12345"

// Collect from Kubernetes pod
client.CollectCoverageFromPod(ctx, podName, "my-test", 9095)

// Option 1: Use convenience method (automatically filters coverage_server.go)
client.ProcessCoverageReports("my-test")

// Option 2: Manual control over each step
client.GenerateCoverageReport("my-test")
client.FilterCoverageReport("my-test")  // Uses default filters
client.GenerateHTMLReport("my-test")

// Option 3: Custom filtering
client.FilterCoverageReport("my-test", "coverage_server.go", "test_helper.go")
```

#### Pod Discovery

The client can automatically discover pods using Kubernetes label selectors, eliminating the need for manual pod name lookup:

```go
// Simple pod discovery (uses default context)
podName, err := client.GetPodName("app=my-app")

// With custom context and timeout
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
podName, err := client.GetPodNameWithContext(ctx, "app=my-app,version=v1.0")

// The method will:
// - List all pods matching the label selector
// - Find the first pod in "Running" state
// - Return an error if no running pods are found
```

#### Filtering Coverage Data

By default, the client automatically filters out `coverage_server.go` from reports to avoid including the coverage collection infrastructure itself. You can customize this behavior:

```go
// Add additional files to filter
client.AddDefaultFilter("internal/test_helper.go")

// Replace default filters
client.SetDefaultFilters([]string{"coverage_server.go", "mock_*.go"})

// Disable filtering (pass empty slice)
client.FilterCoverageReport("my-test", []string{}...)
```

#### Path Remapping

The client automatically detects and remaps container paths (e.g., `/app/example_app.go`) to local filesystem paths. This uses intelligent matching based on relative path structure:

```go
// Path remapping is enabled by default
client.ProcessCoverageReports("my-test")

// Configure source directory (defaults to current working directory)
client.SetSourceDirectory("/path/to/my/project")

// Disable path remapping if needed
client.SetPathRemapping(false)
```

**How it works:**
1. The client analyzes coverage paths that don't exist locally (e.g., `/app/example_app.go`)
2. Matches them to local files by comparing path structures
3. Automatically determines the mapping (e.g., `/app/` → `/Users/user/project/`)
4. Rewrites coverage data to use local paths

This allows tools like `go tool cover` to find source files and generate HTML reports with proper source code display.

### 3. Upload Coverage to Codecov (Optional)

Coverage data can be easily uploaded to Codecov via GitHub Actions. See the [workflow example](https://github.com/psturc/go-coverage-http/blob/main/.github/workflows/test-kind.yml) in this repository.

## Complete Example

This repository includes a working demo application. To try it:

```bash
# Build Docker image with coverage enabled
docker build -f Dockerfile --build-arg ENABLE_COVERAGE=true -t localhost/coverage-http-demo:test .

# Deploy to Kubernetes
kubectl apply -f k8s-deployment.yaml

# Run E2E tests (will collect coverage automatically)
cd test && go test -v
```

The E2E tests will:
- Execute requests against the running pod
- Collect coverage data via port-forwarding
- Generate text and HTML reports in `./coverage-output/`

### Example Files

- `example_app.go` - Sample HTTP server with test endpoints
- `Dockerfile` - Production build (downloads `coverage_server.go` from GitHub)
- `Dockerfile.local` - Local development build (uses local `coverage_server.go`)
- `k8s-deployment.yaml` - Kubernetes deployment manifest
- `test/e2e_test.go` - E2E tests with coverage collection

## API Endpoints

**Application endpoints:**
- `:8000/health` - Health check
- `:8000/greet?name=X` - Greeting endpoint
- `:8000/calculate` - Calculation endpoint

**Coverage endpoints (test builds only):**
- `:9095/coverage` - Collect coverage data
- `:9095/health` - Coverage server health check

## Additional Documentation

- **[TECHNICAL.md](TECHNICAL.md)** - Deep dive into architecture, algorithms, binary formats, and implementation details
- **[Example Workflow](.github/workflows/test-kind.yml)** - Complete CI/CD pipeline with coverage collection and Codecov upload
- **[Example Test](test/e2e_test.go)** - Full e2e test implementation with coverage collection

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.