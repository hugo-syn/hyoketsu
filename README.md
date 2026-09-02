# hyoketsu

Offline identification of DLLs and JARs. Separate known open-source libraries from custom code during reverse engineering and source code review.

Identifies files using three methods:
1. **Microsoft runtime detection** — .NET public key token extraction
2. **Hash matching** — SHA256 (NuGet) and SHA1 (Maven Central) exact match
3. **Filename matching** — fallback against 12M+ DLLs and 14M+ JARs

## Install

Requires Go 1.22+.

```
go build -o hyoketsu .
```

## Database

Stored at `~/.hyoketsu/hyoketsu.db` by default. Use the `--db` flag on any command to point to a different database file:

```
./hyoketsu --db /path/to/hyoketsu.db scan /path/to/binaries
./hyoketsu --db /path/to/hyoketsu.db stats
```

When `--db` is specified, the automatic download prompt on first scan is skipped and the given path is used directly.

### Download pre-built (recommended)

```
./hyoketsu update
```

Downloads the latest database from the Assetnote CDN. Also triggered automatically on first scan if no database exists.

### Build from scratch

Run on a server with good bandwidth.

```
./hyoketsu update --build
```

Runs all steps automatically: Maven crawl, NuGet crawl, hash backfill, and import. The individual NuGet pipeline steps can also be run separately:

```
./hyoketsu crawl-nuget       # Step 1: crawl NuGet catalog to JSONL
./hyoketsu hash-backfill      # Step 2: download nupkgs, compute SHA256 hashes
./hyoketsu import             # Step 3: merge JSONL into SQLite
```

All steps support resuming — re-running skips already completed work.

`--workers` controls concurrency for `crawl-nuget`, `hash-backfill`, and `update --build` (default: 128).

## Usage

### Scan

```
./hyoketsu scan /path/to/binaries

# JSON output
./hyoketsu scan --json /path/to/binaries

# Only unknown files (custom code)
./hyoketsu scan --unknown-only /path/to/binaries

# Only known files (libraries)
./hyoketsu scan --known-only /path/to/binaries

# Only .NET assemblies
./hyoketsu scan --dotnet-only /path/to/binaries

# Hide duplicates (by SHA256)
./hyoketsu scan --dedup /path/to/binaries

# Show only filename-matched files
./hyoketsu scan --filename /path/to/project

# Scan against a remote server (ClickHouse backend)
./hyoketsu scan --remote http://host:8080 /path/to/project
```

`--unknown-only` and `--known-only` are mutually exclusive.

Remote lookups (`scan --remote` and `extract --remote`) classify files as .NET vs. native by binary inspection, but unlike local scans do not mark Microsoft-signed runtime assemblies as pre-known.

## Server

The `server/` directory contains a ClickHouse-backed HTTP server for centralized scanning, exposing `POST /lookup` and `GET /stats`.

```
cd server && go build -o server .
```

Run `docker compose up` to bring up ClickHouse and the server together. The server is started with `-auto-update`, so it downloads the latest prebuilt db from the Assetnote CDN and re-imports it into ClickHouse on startup and then every `-auto-update-interval` (default 720h / 30 days) — no manual `-import-sqlite` step is needed.

Flags:

- `-listen` — HTTP listen address (default `:8080`)
- `-clickhouse` — ClickHouse DSN (default `clickhouse://localhost:9000/default`)
- `-import-sqlite <path>` — one-shot import from a local SQLite db, then exit
- `-import-nuget <dir>` — one-shot import of NuGet JSONL data, then exit
- `-auto-update` — periodically download the latest prebuilt db and re-import it
- `-auto-update-interval` — interval between re-imports, used with `-auto-update` (default 720h)
- `-db-cache` — local path to cache the downloaded db before importing, used with `-auto-update` (default `/data/hyoketsu.db`)

## Extract

Copy unidentified files to a separate directory for decompilation.

```
./hyoketsu extract /path/to/binaries /path/to/output

# Flatten into single directory
./hyoketsu extract --flat /path/to/binaries /path/to/output

# Only .NET, skip dupes
./hyoketsu extract --dotnet-only --dedup /path/to/binaries /path/to/output

# Extract against a remote server (ClickHouse backend) instead of a local db
./hyoketsu extract --remote http://host:8080 /path/to/binaries /path/to/output
```

### Stats

```
./hyoketsu stats
```
