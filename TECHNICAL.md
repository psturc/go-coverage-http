# Technical Documentation

## Overview

`go-coverage-http` is a solution for collecting Go code coverage from running applications in Kubernetes without requiring filesystem access. It works by:

1. Embedding a lightweight HTTP server that collects coverage data using Go's runtime coverage APIs
2. Providing a client library that retrieves coverage via HTTP and processes it locally
3. Automatically remapping container paths to local filesystem paths for tooling compatibility

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes Pod                            │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ Application Container (built with -cover)              │ │
│  │                                                         │ │
│  │  ┌─────────────────┐    ┌─────────────────────────┐  │ │
│  │  │   Your App      │    │  Coverage Server        │  │ │
│  │  │   :8080         │    │  (coverage_server.go)   │  │ │
│  │  │                 │    │  :9095                  │  │ │
│  │  │  - /health      │    │  - GET /coverage        │  │ │
│  │  │  - /api/...     │    │  - GET /health          │  │ │
│  │  └─────────────────┘    └─────────────────────────┘  │ │
│  │           │                        │                  │ │
│  │           │                        │                  │ │
│  │           └──────────┬─────────────┘                  │ │
│  │                      │                                │ │
│  │            runtime/coverage API                       │ │
│  │         (WriteMeta, WriteCounters)                    │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
                         │
                         │ kubectl port-forward
                         │ (handled by client)
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Local Machine                             │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  Coverage Client (client/client.go)                    │ │
│  │                                                         │ │
│  │  1. Port-forward to pod:9095                          │ │
│  │  2. HTTP GET /coverage                                 │ │
│  │  3. Save binary coverage files (covmeta, covcounters) │ │
│  │  4. Convert to text format (go tool covdata)          │ │
│  │  5. Remap container paths to local paths              │ │
│  │  6. Filter unwanted files                             │ │
│  │  7. Generate HTML report (go tool cover)              │ │
│  │                                                         │ │
│  │  Output: ./coverage-output/<test-name>/               │ │
│  │    - coverage.out         (text format, remapped)     │ │
│  │    - coverage_filtered.out (filtered version)         │ │
│  │    - coverage.html        (visual report)             │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## Component Deep Dive

### 1. Coverage Server (`server/coverage_server.go`)

#### Initialization

The coverage server starts automatically via an `init()` function:

```go
func init() {
    go startCoverageServer()
}
```

This is called before `main()`, ensuring the coverage endpoint is available immediately when the application starts.

#### Runtime Coverage Collection

Uses Go's `runtime/coverage` package (Go 1.20+):

```go
import "runtime/coverage"

// Collect metadata (which functions are instrumented)
var metaBuf bytes.Buffer
coverage.WriteMeta(&metaBuf)

// Collect counters (execution counts)
var counterBuf bytes.Buffer
coverage.WriteCounters(&counterBuf)
```

**Key Technical Points:**

1. **No Filesystem Required**: Data is collected in-memory using `bytes.Buffer`
2. **Hash Extraction**: The metadata contains a 16-byte hash (bytes 16-32) used for filename generation
3. **Atomic Operations**: Coverage counters are updated atomically during execution
4. **Thread-Safe**: The runtime APIs are safe to call from multiple goroutines

#### Binary Format

The coverage data uses Go's internal binary format:

- **Metadata File** (`covmeta.{hash}`):
  - Magic number: `0x6d657461` ("meta")
  - Version information
  - Package information
  - Function definitions
  - Source file mappings

- **Counters File** (`covcounters.{hash}.{pid}.{timestamp}`):
  - Magic number: `0x636f756e` ("coun")
  - Execution counters for each instrumented block
  - Indexed by function and block ID

#### HTTP Response Format

Returns JSON with base64-encoded binary data:

```json
{
  "meta_filename": "covmeta.01000000000000000a50ce4bf1a7d569",
  "meta_data": "base64-encoded-metadata",
  "counters_filename": "covcounters.01000000000000000a50ce4bf1a7d569.1.1760850797556279576",
  "counters_data": "base64-encoded-counters",
  "timestamp": 1760850797556279576
}
```

**Why Base64?**
- JSON cannot directly contain binary data
- Base64 encoding is efficient and widely supported
- Preserves exact binary structure without corruption

### 2. Coverage Client (`client/client.go`)

#### Port Forwarding Implementation

Uses the official Kubernetes Go client with SPDY protocol:

```go
transport, upgrader, err := spdy.RoundTripperFor(c.restConfig)
dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", serverURL)
forwarder, err := portforward.New(dialer, ports, stopChan, readyChan, out, errOut)
```

**Technical Details:**

1. **SPDY Protocol**: Required by Kubernetes API for streaming connections
2. **Dynamic Port Allocation**: Uses port `0` to let the OS choose an available local port
3. **Goroutine Management**: Port-forward runs in a separate goroutine, cleaned up via `stopChan`
4. **Ready Signal**: Waits for `readyChan` before proceeding with HTTP requests

#### Binary to Text Conversion

Uses Go's `covdata` tool:

```bash
go tool covdata textfmt -i=<binary-dir> -o=<output-file>
```

**Text Format** (used by most Go tooling):
```
mode: atomic
/path/to/file.go:10.2,12.3 2 5
/path/to/file.go:12.3,15.4 3 0
```

Format: `<file>:<start-line>.<start-col>,<end-line>.<end-col> <num-statements> <count>`

#### Path Remapping Algorithm

**Problem**: Coverage data contains container paths (e.g., `/app/example_app.go`) but local tools expect local paths (e.g., `/Users/user/project/example_app.go`).

**Solution**: Intelligent path matching algorithm

```
Algorithm: detectContainerPaths(coverageLines)

1. Extract all file paths from coverage report
   
2. Identify container paths (paths that don't exist locally)
   Example: /app/example_app.go → doesn't exist locally
   
3. Scan local source directory for Go files
   Build map: relativePath → absolutePath
   Example: "example_app.go" → "/Users/user/project/example_app.go"
            "server/coverage_server.go" → "/Users/user/project/server/coverage_server.go"
   
4. Match container files to local files
   For each container file:
     - Extract filename
     - Find local files with same filename
     - Calculate match score by comparing path suffixes
     
   Example matching:
     Container: /app/example_app.go
     Parts: ["", "app", "example_app.go"]
     
     Local: /Users/user/project/example_app.go
     Parts: ["", "Users", "user", "project", "example_app.go"]
     
     Matching from end: ["example_app.go"] → score = 1
     
5. Extract container root and local root
   From multiple matches, determine common prefixes:
   
   Match 1: /app/example_app.go → /Users/user/project/example_app.go
     Container root: /app/
     Local root: /Users/user/project/
     
   Match 2: /app/server/coverage_server.go → /Users/user/project/server/coverage_server.go
     Container root: /app/
     Local root: /Users/user/project/server/
     
   Select shortest local root (closest to project root): /Users/user/project/
   
6. Apply mapping to all coverage paths
   Replace: /app/ → /Users/user/project/
```

**Key Insights:**

1. **Suffix Matching**: Matches based on path structure from filename backwards
2. **Multiple Candidates**: Analyzes all matches to find the common ancestor
3. **Shortest Path Selection**: Chooses the root closest to the project base
4. **No Hardcoded Paths**: Fully automatic detection based on file structure

#### Filtering Implementation

Simple string matching against coverage report lines:

```go
for _, line := range coverageLines {
    shouldFilter := false
    for _, pattern := range filterPatterns {
        if strings.Contains(line, pattern) {
            shouldFilter = true
            break
        }
    }
    if !shouldFilter {
        filteredLines = append(filteredLines, line)
    }
}
```

**Default Filters:**
- `coverage_server.go` - The instrumentation code itself

**Why Filter?**
- Coverage of the coverage server is not meaningful
- Reduces noise in reports
- Improves coverage percentage accuracy

### 3. Pod Discovery

Uses Kubernetes List API with label selectors:

```go
pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
    LabelSelector: "app=my-app",
})
```

**Algorithm:**

1. List all pods matching the label selector
2. Filter for pods in `Running` phase
3. Return the first running pod
4. If none running, return error with status of first pod

**Label Selector Syntax:**

- Simple: `app=my-app`
- Multiple labels: `app=my-app,version=v1.0`
- Operators: `app!=old-app`, `env in (prod,staging)`

## Build-Time Integration

### Compiler Flags

```bash
go build -cover -covermode=atomic -o app main.go coverage_server.go
```

**Flags Explained:**

- `-cover`: Enable coverage instrumentation
- `-covermode=atomic`: Use atomic counters (thread-safe, recommended for concurrent code)
- Alternative modes:
  - `set`: Boolean coverage (was line executed?)
  - `count`: Simple counters (not thread-safe)
  - `atomic`: Atomic counters (thread-safe, slight overhead)

### Coverage Instrumentation

The compiler automatically transforms code:

**Original:**
```go
func Calculate() int {
    x := 5
    y := 10
    return x + y
}
```

**Instrumented (conceptual):**
```go
func Calculate() int {
    GoCover.Count[42]++ // Block 0 entry
    x := 5
    y := 10
    GoCover.Count[43]++ // Block 1
    return x + y
}
```

**Impact:**
- Small performance overhead (typically <5%)
- Increased binary size (~10-20%)
- No functional changes to code

## File Formats and Specifications

### Coverage Report Format

**Mode Line:**
```
mode: atomic
```

Indicates the coverage mode used during compilation.

**Coverage Line:**
```
/path/to/file.go:10.2,12.16 3 5
```

Fields:
1. File path: `/path/to/file.go`
2. Start position: `10.2` (line 10, column 2)
3. End position: `12.16` (line 12, column 16)
4. Statement count: `3` (3 statements in this block)
5. Execution count: `5` (block executed 5 times)

### Coverage Percentage Calculation

```
Coverage % = (Covered Statements / Total Statements) × 100

Where:
- Covered Statements = statements with count > 0
- Total Statements = sum of all statement counts in report
```

## Kubernetes Integration

### Service Account Permissions

The client requires these Kubernetes RBAC permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: coverage-client
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list"]
- apiGroups: [""]
  resources: ["pods/portforward"]
  verbs: ["create"]
```

### kubeconfig Discovery

Priority order:
1. `KUBECONFIG` environment variable
2. `~/.kube/config`
3. In-cluster configuration (`/var/run/secrets/kubernetes.io/serviceaccount/`)

### Network Requirements

- Kubernetes API server must be reachable
- Pods must allow port-forward connections
- Coverage port (default 9095) must not be firewalled
- No need for service exposure (port-forward creates direct connection)

## Comparison with Traditional Approaches

### GOCOVERDIR Approach

**Traditional Method:**
```yaml
env:
- name: GOCOVERDIR
  value: /coverage-data
volumeMounts:
- name: coverage
  mountPath: /coverage-data
volumes:
- name: coverage
  emptyDir: {}
```

**Drawbacks:**
- Requires writable filesystem in container
- Need volume mounts in deployment
- Must extract files from pod after tests
- Cluster-specific storage configuration
- Persistence concerns

### go-coverage-http Approach

**No Configuration Needed:**
- No environment variables
- No volume mounts
- No deployment changes
- No file extraction

**Benefits:**
- Works in read-only root filesystems
- Compatible with distroless images
- No cluster storage requirements
- Works in any Kubernetes environment

## Performance Considerations

### Coverage Server

- **Memory Usage**: Proportional to code size
  - Typical: 1-5 MB for metadata
  - Typical: 100-500 KB for counters
  - Grows with number of packages and functions

- **CPU Impact**: Minimal
  - Counter updates are atomic operations (nanoseconds)
  - Collection happens only when endpoint is called
  - No continuous background work

- **Network**: Single HTTP request
  - One request per coverage collection
  - Payload size: typically 1-10 MB (base64 encoded)
  - No streaming required

### Client

- **Port Forward**: ~10-50ms setup time
- **Data Transfer**: Depends on coverage size (usually <10 MB)
- **Processing Time**:
  - Binary to text conversion: <1s for typical projects
  - Path remapping: <100ms
  - HTML generation: 1-5s for typical projects

### Build-Time Impact

- **Compilation**: 10-20% slower with `-cover`
- **Binary Size**: 10-20% larger
- **Runtime Performance**: 2-5% overhead from instrumentation

## Troubleshooting

### Common Issues

**1. "No pods found with label selector"**
- Solution: Verify label selector matches pod labels
- Check: `kubectl get pods -l app=my-app -n namespace`

**2. "Port forward failed"**
- Cause: Network issues or RBAC permissions
- Check: `kubectl auth can-i create pods/portforward`

**3. "Failed to generate HTML report: can't read file"**
- Cause: Path remapping didn't work
- Solution: Set correct source directory with `SetSourceDirectory()`
- Enable debug output to see path matching details

**4. "Coverage server not responding"**
- Cause: App not built with `-cover` flag
- Solution: Verify binary was compiled with coverage enabled
- Check: Coverage server should log startup message

### Debug Mode

Enable verbose output by examining console logs:
- Path remapping details (detected paths, match scores)
- Port forward status
- File discovery information

## Security Considerations

### Coverage Server

- **Exposure**: Should only be accessible in test environments
- **Authentication**: No built-in auth (relies on network isolation)
- **Data Sensitivity**: Coverage data may reveal code structure
- **Recommendation**: Never expose coverage port in production

### Client

- **Credentials**: Uses kubeconfig credentials
- **Permissions**: Requires list pods and port-forward access
- **Data Storage**: Coverage data stored locally (protect appropriately)

## Limitations

1. **Go Version**: Requires Go 1.20+ (for `runtime/coverage` package)
2. **Coverage Mode**: Only supports "atomic" mode for Kubernetes deployments
3. **File System**: Path remapping requires local source code access
4. **Single Collection**: Each HTTP request provides snapshot at that moment
5. **Concurrent Access**: Coverage server handles one request at a time

## Future Enhancements

Potential improvements:

1. **Coverage Reset**: Endpoint to reset counters between test runs
2. **Incremental Coverage**: Collect coverage deltas
3. **Multi-Pod Aggregation**: Combine coverage from multiple replicas
4. **Real-time Streaming**: WebSocket-based continuous coverage
5. **Authentication**: Optional token-based auth for coverage endpoint
6. **Metrics Export**: Prometheus metrics for coverage percentage

## References

- [Go Coverage Profiling](https://go.dev/blog/coverage)
- [runtime/coverage package](https://pkg.go.dev/runtime/coverage)
- [Go tool covdata](https://pkg.go.dev/cmd/covdata)
- [Kubernetes Port Forwarding](https://kubernetes.io/docs/tasks/access-application-cluster/port-forward-access-application-cluster/)
- [SPDY Protocol](https://www.chromium.org/spdy/)

## License

See [LICENSE](LICENSE) file for details.

