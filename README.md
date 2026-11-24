# IDL — Instagram Downloader

**Download entire Instagram profiles for free.**

Your all-in-one Instagram downloader for photos, videos, carousels, reels, stories and highlights. A fast, lightweight tool that runs entirely on your computer.

No subscriptions. No credits. No paywalls.

## Features

- Full profile media downloads
- Photos, videos, carousels, reels, stories and highlights
- Best available media quality
- Automatic duplicate detection
- Automatically skips previously downloaded files
- Media integrity verification
- Completely free and locally operated

## Getting Started

### Download — Recommended

Download the latest precompiled binary from the [Releases](https://github.com/baptistax/idlxd/releases) page. No additional software or Go installation is required.

### Session Setup

IDL uses your existing Instagram session to access media.

1. Install the [Cookie-Editor](https://cookieeditor.org/en) browser extension.
2. Open Instagram and make sure you're logged in to your own account.
3. Open Cookie-Editor and export your session cookies in **Netscape** or **JSON** format.
4. Save the exported content as `cookies.txt`.
5. Place `cookies.txt` alongside the IDL executable.

Your session file is sensitive. Keep it private and never share or upload it.

### Usage

Run IDL with the target Instagram username:

```sh
idl <username>
```

All downloaded media is saved to `out/<username>/`.

To check your local setup:

```sh
idl status
```

### Build from Source — Alternative

Only required if you prefer to compile IDL yourself.

Requires Go 1.24 or later.

```sh
go build ./cmd/idl
```

## Privacy & Availability

IDL runs locally and requires no external backend. Only download content you have permission to save. Media availability depends on Instagram access and compatibility.

---

**Your favorite content. One tool. Zero cost.**