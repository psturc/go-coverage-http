# Coverage Strategy: Codecov + SonarCloud

This document explains how we handle different types of test coverage using both Codecov and SonarCloud.

## TL;DR

- **Codecov**: Use **flags** to see unit vs e2e coverage separately ✅
- **SonarCloud**: Shows **merged/combined** coverage for overall quality metrics ✅

## Coverage Flow

```
┌─────────────────┐
│  Unit Tests     │──→ coverage-unit.out ──→ Codecov (flag: unit-tests)
│  (./client/...) │                            ↓
└─────────────────┘                            ↓
                                               ↓
┌─────────────────┐                            ↓
│  E2E Tests      │──→ coverage-e2e.out  ──→ Codecov (flag: e2e-tests)
│  (./test/...)   │         │                  ↓
└─────────────────┘         │                  ↓
                            │                  ↓
                            └──→ MERGE ──→ coverage.out ──→ SonarCloud
```

## Why This Approach?

### Codecov Flags = Granular View
Codecov's flags feature allows you to:
- ✅ **Toggle between test types** in the UI
- ✅ **Compare coverage** between unit and e2e tests
- ✅ **See which lines** are covered by which test type
- ✅ **Track coverage trends** per test type over time

Example Codecov view:
```
All Tests:     75%
├─ unit-tests: 60%  (client library coverage)
└─ e2e-tests:  85%  (integration coverage)
```

### SonarCloud = Overall Quality
SonarCloud shows combined coverage because it focuses on:
- 📊 **Overall code quality metrics**
- 🐛 **Bug detection**
- 🔒 **Security vulnerabilities**
- 💡 **Code smells and maintainability**
- 📈 **Technical debt**

SonarCloud doesn't distinguish between test types because its goal is to answer: **"Is this line covered by ANY test?"**

## Configuration

### In the Workflow (`.github/workflows/test-kind.yml`)

```yaml
# Step 1: Run unit tests
- name: Run unit tests with coverage
  run: go test ./client/... -coverprofile=coverage-unit.out -covermode=atomic

# Step 2: Run E2E tests
- name: Run E2E Go Tests
  run: |
    cd test
    go test -v ./...
    cp coverage-output/e2e-tests/coverage_filtered.out ../coverage-e2e.out

# Step 3: Merge for SonarCloud
- name: Merge coverage reports
  run: |
    echo "mode: atomic" > coverage.out
    grep -h -v "^mode:" coverage-unit.out coverage-e2e.out >> coverage.out

# Step 4: Upload to Codecov with flags
- name: Upload unit test coverage to Codecov
  uses: codecov/codecov-action@v4
  with:
    files: ./coverage-unit.out
    flags: unit-tests

- name: Upload E2E coverage to Codecov
  uses: codecov/codecov-action@v4
  with:
    files: ./coverage-e2e.out
    flags: e2e-tests

# Step 5: Upload merged coverage to SonarCloud
- name: SonarCloud Scan
  uses: SonarSource/sonarcloud-github-action@master
```

### In `sonar-project.properties`

```properties
# SonarCloud uses the merged coverage file
sonar.go.coverage.reportPaths=coverage.out
```

## Viewing Results

### Codecov
1. Go to your [Codecov dashboard](https://codecov.io/gh/psturc/go-coverage-http)
2. Click on **"Flags"** in the left sidebar
3. Toggle between:
   - `unit-tests` - Coverage from unit tests only
   - `e2e-tests` - Coverage from E2E tests only
   - Combined view - All coverage together

### SonarCloud
1. Go to your [SonarCloud project](https://sonarcloud.io/dashboard?id=psturc_go-coverage-http)
2. View overall coverage percentage
3. See which lines need coverage (shows as "uncovered" even if they're tested)
4. Focus on code quality issues, not test type separation

## Alternative Approaches (Not Recommended)

### ❌ Option 1: Separate SonarCloud Projects
Create `psturc_go-coverage-http-unit` and `psturc_go-coverage-http-e2e`

**Why not**: 
- Duplicates quality metrics
- Harder to maintain
- Splits your codebase view

### ❌ Option 2: Only Send E2E Coverage to SonarCloud
Only upload `coverage-e2e.out` to SonarCloud

**Why not**:
- Underreports coverage if unit tests cover code that E2E doesn't
- Less accurate quality metrics

### ✅ Current Approach: Best of Both Worlds
- Codecov: Granular view with flags
- SonarCloud: Combined coverage + quality metrics

## Examples

### Example 1: Line Covered by Both
```go
func Add(a, b int) int {
    return a + b  // ← Covered by BOTH unit and e2e tests
}
```

**Codecov shows:**
- unit-tests: ✅ Covered
- e2e-tests: ✅ Covered

**SonarCloud shows:** ✅ Covered (merged)

### Example 2: Line Covered Only by E2E
```go
func HandleRequest(w http.ResponseWriter, r *http.Request) {
    result := Add(1, 2)  // ← Covered ONLY by e2e tests
    fmt.Fprintf(w, "%d", result)
}
```

**Codecov shows:**
- unit-tests: ❌ Not covered
- e2e-tests: ✅ Covered

**SonarCloud shows:** ✅ Covered (merged)

### Example 3: Uncovered Line
```go
func RareEdgeCase() {
    panic("never tested")  // ← NOT covered by any test
}
```

**Codecov shows:**
- unit-tests: ❌ Not covered
- e2e-tests: ❌ Not covered

**SonarCloud shows:** ❌ Not covered

## Best Practices

1. **Use Codecov for coverage analysis**
   - See which test types cover which code
   - Identify gaps in coverage
   - Track trends per test type

2. **Use SonarCloud for code quality**
   - Ensure overall coverage meets thresholds
   - Fix bugs and vulnerabilities
   - Reduce technical debt

3. **Write both unit and E2E tests**
   - Unit tests: Fast, focused, test logic
   - E2E tests: Slow, realistic, test integration

4. **Monitor both dashboards**
   - Codecov: Are we testing all paths?
   - SonarCloud: Is our code high quality?

## Summary

| Feature | Codecov | SonarCloud |
|---------|---------|------------|
| **Separate test types** | ✅ Yes (flags) | ❌ No (merged only) |
| **Coverage trends** | ✅ Per flag | ✅ Overall |
| **Code quality** | ❌ Coverage only | ✅ Comprehensive |
| **Security scanning** | ❌ No | ✅ Yes |
| **Bug detection** | ❌ No | ✅ Yes |
| **PR comments** | ✅ Coverage changes | ✅ Quality issues |

**Bottom line**: Use Codecov flags for coverage granularity, use SonarCloud for code quality. Both tools complement each other! 🎯

