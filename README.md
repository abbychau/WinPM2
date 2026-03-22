# winpm2

`winpm2` is a lightweight PM2-style process manager for Windows, written in Go.

## Why This Exists

PM2 works best on Linux. On Windows, startup and control overhead can be awkward.

`winpm2` focuses on Windows-first behavior:

- Single Go binary (no Node.js runtime required)
- Local named-pipe control channel (low control-plane overhead)
- Per-user auto-start at logon via Windows Run key
- Saved process resurrection (`save` + `--autoload`)
- PM2-style ecosystem JSON compatibility
- Hidden background launch (no extra console popup)
- Process-tree stop on Windows (`taskkill /T /F`)

## Install

```bash
go build -o winpm2.exe
```

Put `winpm2.exe` in a directory included in `PATH` (for example `C:\bin`).

## Commands

```text
winpm2 daemon [--autoload]
winpm2 startup install|uninstall|status

winpm2 start <ecosystem.json|name>
winpm2 stop <name|all|ecosystem.json>
winpm2 restart <name|all|ecosystem.json>
winpm2 delete <name|all|ecosystem.json>

winpm2 list
winpm2 ls
winpm2 describe <name|ecosystem.json>

winpm2 save
winpm2 resurrect
```

## Startup Model

- `startup install` writes `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\winpm2`
- Startup command is `winpm2 daemon --autoload`
- `startup uninstall` removes the same Run key entry

This is per-user logon startup (not pre-logon system service startup).

## State and Logs

- State directory: `~/.winpm2/`
- Saved dump: `~/.winpm2/dump.json`
- Logs: `~/.winpm2/logs/<app>-out.log`, `~/.winpm2/logs/<app>-err.log`

## Ecosystem Example

`example.json`:

```json
{
  "apps": [
    {
      "name": "articles.zkiz.com",
      "script": "swoole-cli",
      "args": ["-S", "0.0.0.0:19998"],
      "cwd": "C:/Repos/articles.zkiz.com",
      "env": {
        "PHP_CLI_SERVER_WORKERS": 4
      },
      "watch": false
    }
  ]
}
```

## Quick Workflow

```bash
winpm2 startup install
winpm2 start example.json
winpm2 save
winpm2 ls
```

## Current MVP Limits

- `watch` is accepted but currently ignored
- Run-key startup retries on next logon if daemon is not running
