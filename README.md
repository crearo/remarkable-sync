# reMarkable Sync

Sync handwritten notebooks from your reMarkable tablet to your computer. Imported PDFs and EPUBs are skipped unless you pass `--all`. Downloads both PDF and RMDOC formats, tracking changes to avoid re-downloading unchanged files.

![Screenshot](screenshot.png)

## Build

```bash
go build -o remarkable-sync main.go
```

## Run

```bash
# Sync all notebooks
./remarkable-sync

# Sync every document, including imported PDFs and EPUBs
./remarkable-sync --all

# List available notebooks
./remarkable-sync list

# Download a specific notebook (full PDF)
./remarkable-sync "My Notebook"

# Download just the last page of a notebook
./remarkable-sync "My Notebook" last
```

The last-page file is saved to `downloads/pdf/{name}_page_{N}.pdf`.

## How It Works

- Fetches document list from `http://10.11.99.1/documents/`
- Compares timestamps with local manifest (`.sync_manifest.json`)
- Runs two passes: every RMDOC first (a quick copy of the raw files), then every PDF (rendered on the tablet, slow)
- Downloads only documents that are new or modified since that format last synced
- Saves the manifest after each download, so an interrupted sync keeps its progress
- Saves files to `downloads/pdf/` and `downloads/rmdoc/`
- Retries failed downloads (3 attempts)
- Exits early if tablet becomes unreachable

## Requirements

- reMarkable tablet connected to the same network
- Go 1.16+ (install via `brew install go`)
- **Enable USB web interface on your reMarkable:**
  - Go to **Settings > Storage > USB web interface**
  - Toggle it ON
  - Your tablet should be accessible at `http://10.11.99.1`
