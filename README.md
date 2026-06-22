# m3u8-saver

CLI tool for archiving HLS resources from known `archive.json` `video_url` values and future date-based `index.m3u8` URLs.

## What it does

- Reads a JSON config file
- Reads `video_url` values from `archiveJsonPath` when configured
- Builds future date prediction URLs as `{baseUrl}/archive/hls/{yyyymmdd}/index.m3u8`
- Deduplicates targets by URL and local key
- In `dryRun`, checks whether `index.m3u8` exists
- In normal mode, saves:
  - `index.m3u8`
  - referenced child playlists using their original relative paths
  - referenced media files

The tool is focused on saving source assets. It does not transcode, mux, or rewrite playlists.

`video_url` may be a relative path such as `/archive/hls/20251113-2nd/index.m3u8` or a UUID-based path. Relative `video_url` values are resolved against `baseUrl`.

## Config

See `config.example.json`.

`downloadAllVariants` controls HLS variant selection. When `false`, the tool saves only the selected audio playlist and the highest `BANDWIDTH` video playlist. When `true`, it saves every audio and video variant referenced by the master playlist.

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
