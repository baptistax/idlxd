# idl

A small, cookie-based Instagram downloader written in Go.

It currently downloads:
- **Posts / Reels** (timeline media)
- **Highlights** (story highlights)

> ⚠️ Use responsibly. Download only content you have the right to access and comply with Instagram's Terms of Use.

---

## Quick start (recommended): download the binary

This is the simplest way to use `idl`: download the prebuilt binary from the **GitHub Releases** page.

### 1) Download

Download the asset for your platform:

- **Linux (x86_64)**: `idl_linux_amd64`
- **Windows (x86_64)**: `idl_windows_amd64.exe`

### 2) Put `cookies.txt` next to the binary

`idl` expects a **Netscape format** cookies export named `cookies.txt` in the **same folder** as the executable.

Folder example:

```
idl/
  idl_linux_amd64        # or idl_windows_amd64.exe
  cookies.txt
```

### 3) Run

Linux:

```bash
chmod +x ./idl_linux_amd64
./idl_linux_amd64 <username>
```

Windows (PowerShell / CMD):

```powershell
./idl_windows_amd64.exe <username>
```

Downloads are saved under `out/<username>/`.

---

## Build from source (manual compilation)

### Requirements

- **Go 1.22+**
- A valid **Instagram session cookies** file in **Netscape** format

### Clone and build

```bash
git clone <YOUR_REPO_URL>
cd idl
go build -o idl ./cmd/idl
./idl <username>
```

### Dev option: go run

```bash
go run ./cmd/idl <username>
```

---

## Cookies file (cookies.txt)

`idl` expects a **Netscape cookies.txt** export.

Typical ways to obtain it:
- Log in to Instagram in your browser.
- Export cookies for `instagram.com` using a cookie export extension/tool.
- Save/export as **Netscape** format and name it **cookies.txt**.

Notes:
- The file can include `#HttpOnly_` lines (supported).
- Comments starting with `#` are ignored.

---

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

---

## Troubleshooting

### "cookies.txt not found"
- Ensure `cookies.txt` exists in the directory where you run the command.

### Empty results / errors fetching profile
- Cookies may be expired. Export a fresh `cookies.txt`.
- Verify the cookies include relevant `instagram.com` entries.

### Rate limits / transient network errors
- Try again later.
- Keep requests reasonable to avoid triggering rate limits.

---

## Maintainers: release workflow (optional)

This repository can be set up to publish Linux/Windows binaries to **GitHub Releases** using GoReleaser.
See `.goreleaser.yaml` and `.github/workflows/release.yml`.
