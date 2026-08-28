# Contributing to FlixFlox

Thanks for your interest in contributing! This document explains how to get set up, the conventions the project follows, and how to submit changes.

## Getting started

### Prerequisites

- Go 1.26+
- MongoDB (or Docker, to run it via `docker compose`)
- FFmpeg available on `PATH`

### Setup

```bash
git clone https://github.com/elvis-segovia/flixflox.git
cd flixflox
cp .env.example .env
go mod download
go run ./cmd/server
```

The API listens on `http://localhost:7777` by default. Alternatively, run everything (API + MongoDB) with:

```bash
docker compose up --build
```

## How to contribute

### Reporting bugs

Open an issue using the **Bug report** template. Include steps to reproduce, expected vs. actual behavior, and your environment (OS, Go version, FFmpeg version, how you're running the app).

### Suggesting features

Open an issue using the **Feature request** template before starting significant work, so the approach can be discussed first.

### Submitting changes

1. Fork the repository and create a branch from `main`:
   ```bash
   git checkout -b feat/my-feature
   ```
2. Make your changes, following the conventions below.
3. Verify everything builds and passes:
   ```bash
   gofmt -l .        # should print nothing
   go vet ./...
   go test ./...
   go build ./...
   ```
4. Push your branch and open a pull request against `main`, filling in the PR template.

Keep pull requests focused — one logical change per PR is much easier to review than a grab bag.

## Conventions

### Commit messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: Add background image upload and serving for videos
fix: Fix handleAddEpisode
chore: Update dockerfile
docs: Clarify streaming endpoint behavior
```

Common types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `perf`.

### Code style

- Run `gofmt` on all Go code (most editors do this on save).
- Follow the existing package layout: HTTP handlers in `internal/handlers`, domain models in `internal/models`, middleware in `internal/middleware`, and so on (see the [project layout](README.md#project-layout) in the README).
- Prefer small, focused functions and keep handlers thin — push logic down into the appropriate package.

### API changes

If your change adds or modifies an endpoint, update [`openapi.yml`](./openapi.yml) in the same PR so the spec stays in sync with the implementation. Updating the Postman collection (`flixflox.postman_collection.json`) is appreciated but optional.

### Documentation

Update the README when your change affects configuration variables, routes, or setup steps.

## Security issues

Please do **not** open a public issue for security vulnerabilities. See [SECURITY.md](SECURITY.md) for how to report them privately.

## Code of conduct

By participating in this project you agree to abide by the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
