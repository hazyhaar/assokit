# Assokit

Assokit is an open-source Go (MIT) kit for building association and non-profit websites. It is provided as an importable Go module that you can embed in your own single-binary application.

## Quick Start

See `examples/minimal-asso/` for a demonstration of how to instantiate the application using `pkg/api`.

## Architecture

Assokit relies on a modern 2026 stack:
- Go 1.26
- SQLite (modernc.org/sqlite)
- Chi v5 routing
- Templ views

## Configuration

Configuration is passed via the `api.Options` struct. You must provide a `DBPath` and a `fs.FS` containing your `branding.toml` and markdown files.

## Connectors

*(Placeholder Sprint 2+)*

## Contributing

Pull requests are welcome. Make sure to run `go test ./...` and `go vet ./...` before submitting.

## License

MIT License. See the LICENSE file for details.
