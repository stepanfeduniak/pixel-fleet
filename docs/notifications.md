# Automatic agent notifications

Every tracked session is monitored automatically while Pixel Fleet's background
watcher is running. When an agent switches from working to waiting for input
or idle, you receive a notification. There is no arming key or manual fetch.

Messages describe the session's apparent state; they do not claim that a task
succeeded. They include the session name, machine, and optional Linear issue
link. Terminal output and source code are not sent to Slack.

## Native macOS app (default on Mac)

Run `./install.sh` to install **Pixel Fleet.app** and the `cs` command. Open
Pixel Fleet from Applications and allow its notifications. In **System Settings
→ Notifications → Pixel Fleet**, choose **Persistent / Alerts** and enable
**Show in Notification Center**. This setting is controlled by macOS.

```sh
cs notify test
cs notify test 'your session name'
```

The app has its own icon and menu bar controls. A single-session notification
opens that exact session in Terminal; a digest or a click during a break opens
the dashboard. Old notifications for deleted/replaced sessions also open the
dashboard. Script Editor is no longer involved.

Use the menu bar to pause/resume notifications and optionally enable
**Launch at login**. Pausing persists until resumed; quitting the app also
pauses delivery so the watcher will not immediately relaunch it.

See [macOS app setup and behavior](../macos/README.md).

## Slack (optional)

1. Create a Slack app for your workspace at <https://api.slack.com/apps>.
2. Open **Incoming Webhooks**, enable them, and select **Add New Webhook to
   Workspace**. Choose a channel (a private channel just for you works).
3. Copy the resulting webhook URL. On macOS, run:

   ```sh
   pbpaste | cs notify setup slack
   cs notify test
   ```

   On another system, run `cs notify setup slack`, paste the URL, press Enter,
   then Ctrl-D. The setup command selects Slack and saves the URL with owner-only permissions
   in `~/.config/cs/slack-webhook`. It does not send a message. The test
   command explicitly sends one; it refuses during an active break.

4. Check that the test arrived. Configure that Slack channel's notification
   preferences for all new messages if you want a desktop or mobile alert.

Slack's setup reference:
<https://docs.slack.dev/messaging/sending-messages-using-incoming-webhooks/>.

## Use it

Start Claude or Codex inside a normal Pixel Fleet session and give it work.
Step away whenever you want. Pixel Fleet notifies you after the agent has
been consistently ready for at least 10 seconds (normally about 10–15 seconds
including polling). It stays enabled for every following work cycle.

A session that is already idle or asking a question when first discovered
stays quiet until the watcher observes it working. Moving between idle and
waiting without doing new work does not send repeated notifications.
New sessions are enrolled automatically, including archived sessions.

Detach with **q** whenever you like; the watcher runs independently of the
dashboard. The dashboard has no notification toggle.

Optional CLI controls:

```sh
cs notify status
cs notify off frontend  # mute this session
cs notify on frontend   # enable it again
```

## Link a Linear issue (optional)

Copy an issue's full URL from Linear and attach it to the session:

```sh
cs notify link frontend 'https://linear.app/your-workspace/issue/ENG-123/your-issue'
```

The link persists across notification cycles. Remove it with:

```sh
cs notify link frontend -
```

This version links to the issue. It does not change Linear statuses, post
comments, or require Linear credentials in Pixel Fleet. The agent's own
Linear MCP connection remains separate.

## Breaks and reliability

- All notifications wait while the coding blocker is active. Ready
  sessions are combined into a message after it ends (up to 10 per message).
- If the agent resumes work before delivery, its queued readiness is
  cleared and the watcher waits for its next pause.
- Failed submissions remain queued and retry after a delay. macOS Focus or
  notification settings suppressing an accepted notification do not trigger retries. `cs notify status` shows
  queued notifications, the last observed agent state, the last successful
  submission time, and the last delivery error, without the webhook URL.
- State survives restarts in `~/.config/cs/notifications.json`. A single
  background worker holds a process lock to avoid multiple senders.
  Starting `cs` again restarts a stopped worker and enrolls current sessions.
- An idle worker remains running for future sessions. It does not prevent sleep.
- Your laptop must be awake and the session's local tmux viewer must be
  available. Remote sessions depend on that viewer's SSH connection.
- Detection uses the existing visible-screen heuristics. Very short turns
  between polls can be missed; unusual agent screens or a frozen connection
  can be misclassified. Shells, capture failures, and known connection errors
  are not treated as completion.
- Slack webhooks do not provide an exactly-once delivery guarantee. A crash
  after Slack accepts a message but before local acknowledgement, or an
  ambiguous network timeout, can cause a duplicate on retry.

If notifications stop, run `cs notify status`, verify delivery with
`cs notify test`, and inspect `~/.config/cs/notify.log` or `cs.log`.

## Updating an existing dashboard

After installing the new binary, detach with **q**, then run:

```sh
tmux respawn-pane -k -t cs:dashboard 'cs --dashboard-tui'
cs
```

This restarts only the dashboard; agent sessions keep running. Replace `cs`
in the tmux target if you configured a different `session_name`.
