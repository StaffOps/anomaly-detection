# Contributing

## Workflow

1. Create a feature branch: `feat/short-description`
2. Implement with tests (≥90% coverage)
3. Build via Docker — verify it compiles
4. Run tests via Docker — verify they pass
5. Commit using [Conventional Commits](https://www.conventionalcommits.org/)
6. Open PR for review

## Commit Format

```
<type>(<scope>): <description>

[optional body]
```

**Types**: `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`

**Scopes**: `controller`, `worker`, `ml`, `detection`, `replay`, `config`, `docs`

**Examples:**

```
feat(detection): add disk I/O adaptive metric
fix(correlation): use service_name when pod is empty
docs(replay): add CLI usage examples
test(replay): add golden file test for markdown report
chore(deps): bump golang from 1.22 to 1.23
```

## Code Style

### Go

- Standard Go formatting (`gofmt`)
- `internal/` for all business logic (not importable by external packages)
- Context as first parameter
- Table-driven tests
- `errors.Is/As` for error comparison
- `errgroup` for concurrent operations

### Python

- Type hints everywhere
- `async def` for I/O operations
- `pytest` with `@pytest.mark.asyncio`
- `pyproject.toml` for project metadata

## Adding a New Detection Rule

1. Add the rule to `controller/config.yaml` under the appropriate section
2. Test with replay: `controller --replay --from=24h --config=config.yaml`
3. Verify anomaly count is reasonable
4. Add suppression if needed for known-noisy namespaces

## Adding a New Enrichment Query

1. Add to `pod_bundle` or `service_bundle` in `config.yaml`
2. Use template variables (`$namespace`, `$pod`, `$service_name`)
3. The query result becomes an alert annotation with the bundle item's `name`

## Modifying Protobuf

1. Edit `.proto` files in `controller/proto/` or `ml/proto/`
2. Regenerate Go stubs:
   ```bash
   docker run --rm -v "$(pwd)/controller":/src -w /src \
     golang:1.22-alpine sh -c \
     "go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
      go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && \
      protoc --go_out=. --go-grpc_out=. proto/*.proto"
   ```
3. Regenerate Python stubs:
   ```bash
   docker run --rm -v "$(pwd)/ml":/app -w /app python:3.11-slim sh -c \
     "pip install grpcio-tools -q && \
      python -m grpc_tools.protoc -I proto --python_out=server --grpc_python_out=server proto/*.proto"
   ```

## Versioning

- Controller and ML are versioned independently
- Version bumps only on validated milestones (not per-commit)
- See [Roadmap](../roadmap.md) for milestone criteria

## Documentation

Every user-visible change must update documentation:

- New feature → update relevant docs page
- Config change → update [Configuration](../configuration/index.md)
- New metric → update [Monitoring](../operations/monitoring.md)
- Bug fix → update [Troubleshooting](../operations/troubleshooting.md) if relevant
