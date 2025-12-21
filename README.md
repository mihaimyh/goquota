# goquota

Subscription quota management for Go with anniversary-based billing cycles, prorated tier changes, and pluggable storage.

## Features

- **Anniversary-based billing cycles** - Preserve subscription anniversary dates across months
- **Prorated quota adjustments** - Handle mid-cycle tier changes fairly
- **Multiple quota types** - Support both daily and monthly quotas
- **Pluggable storage** - Firestore, in-memory, or custom backends
- **Transaction-safe** - Prevent over-consumption with atomic operations
- **Optional caching** - Improve performance with configurable caching
- **HTTP middleware** - Easy integration with web frameworks

## Status

 **Work in Progress** - This library is currently being extracted from a production SaaS application.

## Installation

`ash
go get github.com/mihaimyh/goquota
`

## Quick Start

Coming soon...

## Documentation

- [Implementation Plan](https://github.com/mihaimyh/goquota/blob/main/docs/implementation_plan.md)
- [Architecture](https://github.com/mihaimyh/goquota/blob/main/docs/architecture.md)

## License

MIT License - see [LICENSE](LICENSE) for details

## Contributing

Contributions welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.
