# m3u8-saver

CLI tool for archiving HLS resources from date-based `index.m3u8` URLs.

## What it does

- Reads a JSON config file
- Expands `{yyyymmdd}` in `urlTemplate`
- Scans a date range
- In `dryRun`, checks whether `index.m3u8` exists
- In normal mode, saves:
  - `index.m3u8`
  - referenced child playlists using their original relative paths
  - referenced media files

The tool is focused on saving source assets. It does not transcode, mux, or rewrite playlists.

## Config

See [`config.example.json`](config.example.json).

## Run

Dry run:

```bash
go run ./cmd/m3u8-saver ./config.example.json
```

Normal run:

```bash
go run ./cmd/m3u8-saver ./config.json
```

Output prints one line per date and one final summary line.
