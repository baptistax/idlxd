# idl

An Instagram downloader written in Go.

It currently downloads:
- Posts / Reels
- Highlights

Use responsibly. Download only content you have the right to access and comply with Instagram's Terms of Use.

## Cookie workflow

Authentication is done with a file named `cookies.txt`.

Supported formats inside `cookies.txt`:
- Netscape cookies.txt
- Cookie-Editor JSON export

Only these two formats are supported. Other cookie formats are not supported.

Steps:
1. Log in to Instagram in your browser with an active session.
2. Install the Cookie-Editor extension.
3. Export cookies in Netscape or JSON format.
4. Save the exported content to a file named exactly `cookies.txt`.
5. Place `cookies.txt` next to the `idl` executable.
6. Run `idl <username>`.

`idl` resolves `cookies.txt` from the executable directory first, with fallback to the current working directory.

Security warning: do not share `cookies.txt`. Treat cookies like credentials.

## Quick start

1. Download the binary for your platform.
2. Put `cookies.txt` next to the binary.
3. Run `idl <username>`.

Downloads are saved under `out/<username>/`.

## Build from source

Requirements:
- Go 1.22+

On Unix-like systems:

```bash
go build -o idl ./cmd/idl
./idl <username>
```

On Windows:

```powershell
go build -o idl.exe .\cmd\idl
.\idl.exe <username>
```

## CLI output

The CLI starts with an `IDL` banner and shows progress bars for each stage:

```text
+------------------------+
|  ___ ____  _           |
| |_ _|  _ \| |          |
|  | || | | | |          |
|  | || |_| | |___       |
| |___|____/|_____|      |
| Instagram Downloader   |
+------------------------+
Target:     nasa
Output:     out\nasa
Profile ID: 123456789

[1/2] Posts / Reels
-------------------
POSTS / REELS  [########################] 100% 150/150 01:12
Saved: 150 files

[2/2] Highlights
----------------
HIGHLIGHTS     [########################] 100% 133/133 00:54
Saved: 133 files

Finished in 02:06
```

## Output structure

By default, downloads are stored in `out/`:

```text
out/
  <username>/
    posts/
      <timestamp>_<media_id>.jpg
      <timestamp>_<media_id>.mp4
      <timestamp>_<media_id>_01.jpg
      ...
    highlights/
      <highlight_title>/
        <timestamp>_<media_id>_01.jpg
        <timestamp>_<media_id>_02.mp4
        ...
```

Filename format:
- `YYYYMMDD_HHMMSS_<media_id>[_NN].<ext>`
