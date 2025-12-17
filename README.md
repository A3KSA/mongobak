[![Build & Release](https://github.com/A3KSA/mongobak/actions/workflows/go.yml/badge.svg)](https://github.com/A3KSA/mongobak/actions/workflows/go.yml)

# mongobak

Simple, reliable MongoDB backup tool written in Go.

`mongobak` allows you to export MongoDB databases and collections into JSON (Extended JSON)
files, suitable for archiving, inspection, or re-import.

---

## Features

- MongoDB connection testing
- List databases and collections
- JSON backups using MongoDB Extended JSON
- One file per collection or single merged output
- Collection exclusion support
- Cross-platform single binary (Linux, macOS, Windows)

---

## Installation

Download a prebuilt binary from the GitHub releases page: https://github.com/automation3000/mongobak/releases


Example (Linux):

```bash
curl -L https://github.com/automation3000/mongobak/releases/latest/download/mongobak-linux-amd64 \
  -o mongobak
chmod +x mongobak
```

## Usage
Connect to MongoDB
Test the connection and store configuration locally:

```bash
mongobak connect \
  --uri "mongodb://localhost:27017" \
  --db mydatabase
```

Configuration is stored in:

Linux/macOS: ~/.config/mongobak/config.json
Windows: %APPDATA%\mongobak\config.json


List databases and collections
```bash
mongobak list
```

List collections from a specific database:

```bash
mongobak list --db otherdb
```

## Backup database
Backup all collections into a directory (one file per collection):

```bash
mongobak backup --output ./backups
```

Exclude specific collections:

```bash
mongobak backup \
  --exclude logs,tmp \
  --output ./backups
```

Create a single merged output file (JSON Lines):

```bash
mongobak backup --output ./backup.jsonl
```

## Output format
Files are written in MongoDB Extended JSON

One document per line (JSONL)

Compatible with mongoimport

Preserves ObjectId, Date, and other BSON types

Example:

```json

{"_id":{"$oid":"64f1c2..."},"name":"example","createdAt":{"$date":"2025-01-01T12:00:00Z"}}
```

---

## Build from source
Requirements:

Go 1.22+

```bash

git clone https://github.com/automation3000/mongobak.git
cd mongobak
go mod tidy
go build -o mongobak
```

## Versioning
mongobak follows semantic versioning.

Releases are automatically built and published on GitHub when a version tag is pushed:

```bash

git tag v1.0.0
git push origin v1.0.0
```

---

## Checksums

Each release includes a `checksums.txt` file containing SHA-256 hashes
for all published binaries.

---

## License
This project is licensed under the MIT License.


