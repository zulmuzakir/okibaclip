# pastello

A clipboard history manager for macOS 12+, Windows 10/11 and Linux (GTK 3), built with [mygo](https://github.com/egoist/mygo). It lives in the tray or menu bar, records text, rich text, links, colors, images and files, and opens a searchable panel from a global shortcut (default `Alt+Shift+V`).

Choosing an entry copies it and hides the panel; press Cmd/Ctrl+V yourself. There is no keystroke injection and no Accessibility permission. History is a plain SQLite file (mode 0600) in the app's user-data folder; copies flagged as concealed or transient by password managers are skipped.

## Upgrading from my-clipboard

pastello was formerly named my-clipboard, with bundle identifier `com.example.myclipboard`. The first launch moves the old user-data folder over when the new one doesn't exist yet, and old export files still import. The rest is tied to the old identity:

- **Open at login**: the old login entry is separate from pastello's, and toggling the setting in pastello never touches it. Turn "Open at login" off in my-clipboard before upgrading, or remove the old entry (macOS: System Settings › General › Login Items; Windows: Task Manager › Startup; Linux: `~/.config/autostart/com.example.myclipboard.desktop`), then turn it on in pastello.
- **macOS**: the pasteboard permission belongs to the old bundle identifier, so macOS 15.4+ asks again whether pastello may paste from other apps.

## Run

```
go run .            # or: go tool mygo dev
go tool mygo build  # installers and packages in build/
go test ./...       # the first run downloads the SQLite library
```

## Command line

`pastello [--toggle|--show|--hide|--settings|--pause|--resume|--quit]` controls the running instance (a second launch forwards its flags and exits). A bare second launch shows the panel.

## Linux

- Tray icons need `libayatana-appindicator3-1`; without it the app still runs and shows the panel at launch.
- Under Wayland the app forces XWayland (`GDK_BACKEND=x11`) when `DISPLAY` is set, so clipboard polling works. Set `PASTELLO_NATIVE_WAYLAND=1` to opt out; native Wayland only sees copies while a pastello window is focused.
- Global shortcuts may not reach the app on Wayland. Bind a desktop shortcut to `pastello --toggle` instead.

## macOS

macOS 15.4+ may ask whether pastello may read the pasteboard from other apps; choose Always Allow (System Settings › Privacy & Security › Paste from Other Apps).

## Release

Pushing a `v*` tag runs `.github/workflows/release.yml`, which builds every platform and attaches the installers to a draft release. Auto-update is not configured.
