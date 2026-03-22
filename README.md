# winpm2

`winpm2` is a lightweight Windows-first process manager inspired by PM2, implemented in Go.

It is designed to keep IPC and runtime overhead low while still providing PM2-style ecosystem support, process resurrection, and startup automation.

## Why winpm2

- Lower control-plane overhead with local named-pipe IPC
- Single Go binary with no Node.js runtime dependency
- PM2-style ecosystem JSON input (`apps`, `script`, `args`, `cwd`, `env`)
- Process resurrection support (`save` and `resurrect`)
- Per-user startup integration using `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run`

## Installation

Build from source:

```bash
go build -o winpm2.exe
```

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

## Startup Behavior

- `startup install` writes a `winpm2` entry to `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run`
- The entry starts `winpm2 daemon --autoload` at user logon
- `startup uninstall` removes the run key entry

## State and Logs

- State directory: `~/.winpm2/`
- Saved process dump: `~/.winpm2/dump.json`
- Logs: `~/.winpm2/logs/<app>-out.log` and `~/.winpm2/logs/<app>-err.log`

## Ecosystem File Example

Example file is provided at `example.json`.

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

## Promotion Points

- Built specifically for Windows process management
- Lower control-plane overhead than PM2 by using local named pipes
- Per-user startup via Windows Run key with automatic `--autoload` resurrection
- PM2-style ecosystem onboarding with file-based `start/stop/restart/delete`
- No extra console popup when launching daemon or managed apps
- Better operational tooling with `ls` alias and `describe` command
- Stronger stop behavior by killing the full process tree on Windows

## Considerations

- Startup is user-logon triggered (not pre-logon system boot)
- If the daemon crashes, Run-key startup retries on next logon only
- `watch` is currently ignored in MVP
- Child process graceful shutdown semantics depend on process behavior; kill fallback is used
- Keep script paths and working directories explicit to avoid service/session environment differences

## Typical Workflow

```bash
winpm2 startup install
winpm2 start example.json
winpm2 save
winpm2 list
```
