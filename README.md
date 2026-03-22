# winpm2

Windows-first process manager inspired by PM2, built in Go.

`winpm2` is designed for teams that want PM2-style workflow on Windows with lower overhead, cleaner startup behavior, and simple deployment.

## Highlights

- Native Windows focus (not Linux-first behavior ported over)
- Single binary, no Node.js runtime dependency
- Local named-pipe IPC for low control-plane overhead
- PM2-style ecosystem JSON (`apps`, `script`, `args`, `cwd`, `env`)
- Per-user auto-start at logon via Windows Run key
- Auto-resurrect on startup (`save` + `daemon --autoload`)
- Hidden background launch (no extra console windows)
- Process-tree stop on Windows

## Why Use winpm2 Instead of PM2 on Windows

- Startup is straightforward with `HKCU\...\Run`
- Runtime footprint is smaller (Go binary + local IPC)
- Better fit for Windows operational model

| Area | winpm2 | PM2 on Windows |
| --- | --- | --- |
| Runtime dependency | Single Go binary | Requires Node.js + npm environment |
| Startup integration | Built-in `startup install` with HKCU Run key | Usually needs extra startup setup/workarounds |
| Control channel | Local named pipe IPC | Node-based daemon/IPC model |
| Console behavior | Hidden background launch by default | May involve extra console/session behavior |
| Process stop semantics | Supports Windows process-tree stop | Behavior depends on process/session setup |
| Ecosystem migration | PM2-style ecosystem JSON supported | Native PM2 ecosystem format |

## Quick Start

```bash
go build -o winpm2.exe
winpm2 startup install
winpm2 start example.json
winpm2 save
winpm2 ls
```

## Example Ecosystem File

See `example.json`.

## Documentation

- Usage guide: `docs/usage.md`

## Project Status

MVP is working and actively evolving.

Current known limits:

- `watch` is parsed but not implemented yet
- Run-key startup restarts at next logon if daemon is manually killed
