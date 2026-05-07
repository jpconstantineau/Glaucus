# Glaucus

Glaucus is a PocketBase-backed Go application for the roadmap in `spec-golang/`.

## Local Defaults

When Glaucus starts with a fresh local profile, it bootstraps a default operator account for the web UI:

- login URL: `http://127.0.0.1:8090/login`
- default operator email: `admin@glaucus.local`
- default operator password: `glaucus-admin`
- default profile slug: `default`
- default profile root: `profiles/default`
- default PocketBase data dir: `profiles/default/pb_data`

Important behavior:

- the default operator is only created when the `operators` collection is empty
- if an operator record already exists, Glaucus does not reset the password back to `glaucus-admin`
- the login page hint shows the default bootstrap email, but the password may have changed in an existing local profile

## Development

The required merge gate for roadmap work is a green GitHub Actions `ci` run on the branch or pull request. The workflow verifies:

- `gofmt`
- `go vet`
- `go test ./...`
- `go build ./cmd/glaucus` on `ubuntu`, `windows`, and `macos`
