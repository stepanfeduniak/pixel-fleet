# Pixel Fleet for macOS

`./install.sh` from the repository root builds and installs both:

- `~/Applications/Pixel Fleet.app` — native menu bar app, with its own icon,
  notification permission, notification actions, and optional login item.
- `~/.local/bin/cs` — the existing terminal interface and background watcher.

Requires macOS 13+, Apple's command-line developer tools (`swiftc`), and Go.
The installer builds and signs a local app bundle and preserves running agent
sessions. Restart an existing dashboard to load the updated CLI.

Open Pixel Fleet from Applications. Allow notifications, then choose
**Persistent / Alerts** and **Show in Notification Center** for **Pixel Fleet**
in System Settings → Notifications. Its settings are separate from Script
Editor's old notifications, which can be disabled.

Handoffs and test notifications play the standard macOS notification sound.
Enable **Play sound for notification** in Pixel Fleet’s notification settings
and make sure system alert volume is audible. Focus and silent settings still apply.

The menu bar offers:

- **Open dashboard** — opens the dashboard in Terminal.
- **Pause / Resume notifications** — persists across app and watcher restarts.
  Agent sessions keep running. Readiness is delivered after resuming if the
  agent still needs you.
- **Send test notification** and **Notification settings**.
- **Launch at login** — an opt-in macOS login item managed by SMAppService.
  macOS may ask for approval in Login Items. Disable it from the same menu.
- **Quit Pixel Fleet** — pauses notifications and quits the menu bar app.
  Reopen the app and choose Resume when you want notifications again.

Notifications for one session open that exact session in Terminal. Digests
open the dashboard. Breaks and session identity are checked again when the
terminal command runs, so old notifications cannot open a replacement session
with the same name or bypass a break.

Try a targeted notification with:

```sh
cs notify test 'your session name'
```

The Go watcher places a request in `~/.config/cs/app-inbox`. The app submits it
using UserNotifications and acknowledges success or failure. Permission denial
is reported to the watcher and retried, not silently marked as delivered.
Native click actions never launch Script Editor. The app starts a missing
watcher and checks periodically that it is still running.

The included icon is drawn from source in `Icon.swift`. No asset downloads or
third-party application frameworks are required.
