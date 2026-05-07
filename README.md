# Glaucus

Glaucus is a PocketBase-backed Go application for the roadmap in `spec-golang/`.

## Development

The required merge gate for roadmap work is a green GitHub Actions `ci` run on the branch or pull request. The workflow verifies:

- `gofmt`
- `go vet`
- `go test ./...`
- `go build ./cmd/glaucus` on `ubuntu`, `windows`, and `macos`
