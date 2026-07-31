# TimeClicker

TimeClicker is a small Windows tray tool for saving the current time with one click.

Project link: <https://github.com/stevengeyue>

It keeps a compact desktop window for the current time and the last recorded time. Clicking the tray icon brings the window back if it is hidden; clicking again records the time and copies it to the clipboard.

## Features

- Tray icon for quick access
- Small always-on-top desktop window
- One-click record and copy
- Configurable clipboard format
- Persistent local log
- Optional start with Windows
- Single-instance behavior

## Usage

Run `TimeClicker.exe`.

- Left-click the tray icon:
  - shows the window if it is hidden
  - records the current time if the window is already visible
- Right-click the tray icon for:
  - record current time
  - show or hide the desktop window
  - choose the clipboard format
  - set a custom format
  - open the log file
  - open the GitHub link
  - toggle start with Windows
  - exit

The desktop window shows:

- current time
- last recorded time
- active clipboard format

## Data

TimeClicker stores data under:

```text
%LocalAppData%\TimeClicker
```

Records are appended to:

```text
records.ndjson
```

Each line is JSON:

```json
{"local":"2026-07-31 19:39:00","utc":"2026-07-31T11:39:00Z","epochMs":1785497940878,"copiedText":"2026-07-31 19:39:00","formatId":"default_seconds"}
```

Settings are stored in:

```text
settings.json
```

## Clipboard Formats

Built-in formats:

- `yyyy-MM-dd HH:mm:ss`
- `yyyy/MM/dd HH:mm`
- `HH:mm:ss`
- `yyyy-MM-dd`
- `ISO 8601`

Custom formats use the same tokens, for example:

```text
yyyy-MM-dd HH:mm
```

## Build

Requirements:

- Windows
- Go 1.25 or newer

Build:

```powershell
.\build.ps1
```

The executable is written to:

```text
dist\TimeClicker.exe
```

The `rsrc.syso` file embeds the Windows Common Controls manifest required by `walk`.

## License

MIT
