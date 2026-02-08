# IMCS - In-Memory Cache Server (Go)

High-performance, persistent key-value store with TTL support, written in Go.
Inspired by Redis architecture.

## Features

- 🚀 **TCP Protocol**: Custom text-based protocol.
- ⚡ **Concurrency**: Thread-safe operations using `sync.RWMutex`.
- ⏱️ **TTL Support**: Automatic background cleanup of expired keys.
- 💾 **Persistence**: Data durability via Gob snapshots (`SAVE` command).
- 🐳 **Dockerized**: Lightweight Alpine-based image (~15MB).
- ⚙️ **Configurable**: CLI flags for port and storage location.

## Quick Start

### Docker (Recommended)

```bash
docker build -t imcs .
docker run -d -p 8080:8080 -v $(pwd)/data:/root/cache-files imcs