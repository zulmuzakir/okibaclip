# Okiba Clip

*Okiba* (置き場) is Japanese for "a place to set things down", which is what your copies get here.

A clipboard history manager for macOS 12+, Windows 10/11 and Linux (GTK 3), built with [mygo](https://github.com/egoist/mygo). It lives in the tray or menu bar, records text, rich text, links, colors, images and files, and opens a searchable panel from a global shortcut (default `Alt+Shift+V`).

Choosing an entry copies it and hides the panel; press Cmd/Ctrl+V yourself. Keystrokes are sent only if you turn on Paste after choosing in Settings (off by default; macOS then asks for Accessibility). History is a plain SQLite file (mode 0600) in the app's user-data folder; copies flagged as concealed or transient by password managers are skipped.

The user-data folder holds `history.db` and `settings.json`, and Settings › About shows its path:

| OS | Folder |
|---|---|
| macOS | `~/Library/Application Support/okibaclip` |
| Windows | `%LocalAppData%\okibaclip` |
| Linux | `$XDG_DATA_HOME/okibaclip` (usually `~/.local/share/okibaclip`) |

## Search

Every word you type must match, in any order and ignoring case: `git push` finds "push to git". A word matches anywhere in the text, or fuzzily near its start when it has 3+ letters and the letters appear in order close together (`gtpsh` finds "git push"). Whole-word matches rank first, then pinned, then recent. Matches are highlighted, and ↵ / ⇧↵ copy the chosen result straight from the search field.

| Syntax | Meaning |
|---|---|
| `"exact phrase"` | the phrase as typed, no fuzzy matching |
| `type:text` `type:link` `type:color` `type:image` `type:file` | that kind only; several mean any of them |
| `is:pinned` / `is:unpinned` | pinned or unpinned entries |
| `after:` / `before:` + `today`, `yesterday`, `12h`, `7d`, `2w` or `2026-01-31` | by time of last use; dates mean the start of that day |

Snippets search their name and body with the same words; filters apply to history only. Anything that isn't a valid filter is searched for as text.

## Run

```
git clone https://github.com/zulmuzakir/okibaclip.git
cd okibaclip
go run .            # or: go tool mygo dev
go tool mygo build  # installers and packages in build/
go test ./...       # the first run downloads the SQLite library
```

## Command line

`okibaclip [--toggle|--show|--hide|--settings|--pause|--resume|--quit]` controls the running instance (a second launch forwards its flags and exits). A bare second launch shows the panel.

## Linux

- Tray icons need `libayatana-appindicator3-1`; without it the app still runs and shows the panel at launch.
- Under Wayland the app forces XWayland (`GDK_BACKEND=x11`) when `DISPLAY` is set, so clipboard polling works. Set `OKIBACLIP_NATIVE_WAYLAND=1` to opt out; native Wayland only sees copies while an Okiba Clip window is focused.
- With `wl-clipboard` installed on a compositor that supports data-control (KDE, Sway, Hyprland; not GNOME), copies are captured through `wl-paste` even from native Wayland apps. Source-app tracking (ignore list, `app:`) uses the X11 active window, so it covers only X11 apps.
- Global shortcuts may not reach the app on Wayland. Bind a desktop shortcut to `okibaclip --toggle` instead.

## macOS

macOS 15.4+ may ask whether Okiba Clip may read the pasteboard from other apps; choose Always Allow (System Settings › Privacy & Security › Paste from Other Apps).

## Release

Pushing a `v*` tag runs `.github/workflows/release.yml`, which builds every platform and attaches the installers to a draft release. Auto-update is not configured.
