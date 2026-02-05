# idl

An Instagram downloader written in Go.

It currently downloads:
- **Posts / Reels** (timeline media)
- **Highlights** (story highlights)

> ⚠️ Use responsibly. Download only content you have the right to access and comply with Instagram's Terms of Use.

## Cookie workflow (required)

Authentication is done with a file named **`cookies.txt`**.

Supported cookie export formats inside `cookies.txt`:
- **Netscape cookies.txt**
- **Cookie-Editor JSON export**

> Only these two formats are supported. Other cookie formats are **not** supported.
>
> Cookie handling has only been tested with the **Cookie-Editor** browser extension.

### Steps

1. Log in to Instagram in your browser with an active session.
2. Install the **Cookie-Editor** extension.
3. Export cookies in **Netscape** or **JSON** format.
4. Save the exported content to a file named exactly **`cookies.txt`**.
5. Place `cookies.txt` next to the `idl` executable.
6. Run:

```bash
idl <username>
```

`idl` resolves `cookies.txt` from the executable directory (with fallback to the current working directory behavior).

> 🔒 **Security warning:** do not share `cookies.txt`. Treat cookies like passwords/credentials.

## Quick start (binary)

1. Download the binary from releases for your platform.
2. Put `cookies.txt` next to the binary.
3. Run `idl <username>`.

Downloads are saved under `out/<username>/`.

## Build from source

Requirements:
- Go 1.22+

```bash
go build -o idl ./cmd/idl
./idl <username>
```

## Output structure

By default, downloads are stored in `out/`:

```
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
