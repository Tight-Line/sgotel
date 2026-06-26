## What and why

<!-- Describe the change and why it's needed. Link any relevant issues. -->

## Testing

<!-- How was this tested? Unit tests, manual steps, etc. -->

## Checklist

- [ ] `go vet ./...` passes
- [ ] `go test -race ./...` passes
- [ ] New metric labels don't include high-cardinality fields (email, message ID)
- [ ] Config changes are via env vars and documented in the README
