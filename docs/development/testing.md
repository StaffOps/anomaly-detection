# Testing

## Go Tests

```bash
# Run all tests
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine go test ./...

# Verbose output
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine go test -v ./...

# Specific package
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine go test -v ./internal/replay/...

# With coverage
docker run --rm -v "$(pwd)/controller":/src -w /src golang:1.22-alpine sh -c \
  "go test ./... -coverprofile=cov.out -covermode=atomic && go tool cover -func=cov.out | tail -1"
```

## Python Tests

```bash
# Run all tests
docker run --rm -v "$(pwd)/ml":/app -w /app python:3.11-slim sh -c \
  "pip install -e '.[dev]' -q && pytest tests/ -v"

# With coverage
docker run --rm -v "$(pwd)/ml":/app -w /app python:3.11-slim sh -c \
  "pip install -e '.[dev]' -q && pytest --cov=server --cov-report=term-missing tests/"
```

## Test Structure

### Go (table-driven)

```go
func TestParseWindow(t *testing.T) {
    tests := []struct {
        name    string
        from    string
        to      string
        wantErr bool
    }{
        {"relative duration", "24h", "now", false},
        {"absolute timestamp", "2026-05-30T00:00:00Z", "2026-05-30T12:00:00Z", false},
        {"from after to", "1h", "24h", true},
        {"too short window", "10m", "now", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, _, err := ParseWindow(tt.from, tt.to, 7*24*time.Hour)
            if (err != nil) != tt.wantErr {
                t.Errorf("ParseWindow() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Python (pytest)

```python
@pytest.mark.asyncio
async def test_detect_multivariate_returns_score():
    detector = MultivariateDetector()
    features = [0.9, 0.8, 2.0, 0.05, 150.0]
    result = await detector.detect(features)
    assert 0.0 <= result.score <= 1.0
```

## Integration Tests

Integration tests require the full stack running and use build tag `integration`:

```bash
# Run integration tests (requires stack up)
docker run --rm --network=host \
  -v "$(pwd)/controller":/src -w /src golang:1.22-alpine \
  go test -tags=integration ./internal/replay/...
```

!!! info "Integration test status"
    Integration tests for replay mode (T13) are pending. They will inject synthetic data into VM/Loki and verify detection accuracy.

## Golden File Tests

Report serializers (JSON, Markdown) use golden file testing:

```go
func TestReportJSON(t *testing.T) {
    report := buildTestReport()
    var buf bytes.Buffer
    report.WriteJSON(&buf)

    golden := filepath.Join("testdata", "report.golden.json")
    if *update {
        os.WriteFile(golden, buf.Bytes(), 0644)
    }
    expected, _ := os.ReadFile(golden)
    assert.JSONEq(t, string(expected), buf.String())
}
```

Update golden files with: `go test ./... -update`

## Coverage Target

Minimum **90% line coverage** per the project's development standards. Current coverage areas:

| Package | Tests | Coverage |
|---------|-------|----------|
| `internal/replay/window` | 22 sub-tests | ✅ |
| `internal/replay/inmem_baseline` | 7 tests | ✅ |
| `internal/replay/adapter` | 7 tests | ✅ |
| `internal/replay/report` | Golden file tests | ✅ |
| `internal/correlation/workload` | 15 tests | ✅ |
| `internal/readiness/` | 7 tests | ✅ |
| `internal/enrichment/links` | 6 tests | ✅ |
