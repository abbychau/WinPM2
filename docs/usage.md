# winpm2 Usage Guide

This document covers install, startup, command usage, and runtime behavior for `winpm2`.

## Install

Build:

```bash
go build -o winpm2.exe
```

Recommended deployment:

1. Place `winpm2.exe` in a directory in `PATH` (for example `C:\bin`)
2. Open a new shell and verify:

```bash
winpm2 ls
```

## Startup Integration (Per-User)

`winpm2` uses:

- `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\winpm2`

Commands:

```bash
winpm2 startup install
winpm2 startup status
winpm2 startup uninstall
```

Behavior:

- `startup install` writes a Run entry to launch `winpm2 daemon --autoload` at user logon
- `startup uninstall` removes that entry
- Startup is per-user logon startup (not pre-logon machine service startup)

## Core Commands

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

Notes:

- `ls` is an alias for `list`
- `stop/restart/delete` can target a single app name, `all`, or an ecosystem file
- `describe` requires exactly one resolved app target

## Typical Workflow

```bash
winpm2 start example.json
winpm2 ls
winpm2 describe articles.zkiz.com
winpm2 save
```

After next logon, daemon autoload restores saved apps.

## Ecosystem File

Example (`example.json`):

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

Supported fields in MVP:

- `name`
- `script`
- `args`
- `cwd`
- `env`
- `autorestart`

Current limitation:

- `watch` is parsed but ignored

## Data and Logs

- State directory: `~/.winpm2/`
- Saved process dump: `~/.winpm2/dump.json`
- Logs:
  - `~/.winpm2/logs/<app>-out.log`
  - `~/.winpm2/logs/<app>-err.log`

## Process Behavior

- Daemon and managed apps are launched without popping an extra console window
- `stop` uses process-tree termination on Windows
- Crash-loop protection and restart backoff are enabled in supervisor logic

## Troubleshooting

- **`describe` returns unknown command**
  - You are likely talking to an older daemon process
  - Restart daemon once (`taskkill /IM winpm2.exe /F`), then run command again

- **`stop` appears ineffective**
  - Ensure you are using latest binary and daemon has been restarted
  - Use `winpm2 describe <app>` to confirm status and pid

- **Startup not working after install**
  - Confirm `startup status`
  - Confirm current user logon occurred after install
