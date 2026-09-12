import AppKit
import UserNotifications
import ServiceManagement

struct Handoff: Codable {
    let id: String
    let body: String
    let session: String
    let createdAt: String
}

final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate, NSMenuDelegate {
    let directory = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".config/cs")
    var inbox: URL { directory.appendingPathComponent("app-inbox") }
    var pauseFile: URL { directory.appendingPathComponent("notifications-paused") }
    var cli: URL { Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/cs") }
    var statusItem: NSStatusItem!
    var timer: Timer?
    var workerTimer: Timer?
    var inFlight = Set<String>()
    var pauseItem: NSMenuItem!
    var loginItem: NSMenuItem!
    var permissionItem: NSMenuItem!
    let center = UNUserNotificationCenter.current()

    func applicationDidFinishLaunching(_ notification: Notification) {
        try? FileManager.default.createDirectory(at: inbox, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        center.delegate = self
        center.setNotificationCategories([
            UNNotificationCategory(identifier: "SESSION", actions: [UNNotificationAction(identifier: "OPEN", title: "Open session", options: [.foreground])], intentIdentifiers: []),
            UNNotificationCategory(identifier: "DASHBOARD", actions: [UNNotificationAction(identifier: "OPEN", title: "Open dashboard", options: [.foreground])], intentIdentifiers: [])
        ])
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        statusItem.button?.image = NSImage(systemSymbolName: "square.grid.2x2.fill", accessibilityDescription: "Pixel Fleet")
        statusItem.button?.toolTip = "Pixel Fleet"
        let menu = NSMenu()
        menu.delegate = self
        menu.addItem(item("Open dashboard", #selector(openDashboard)))
        menu.addItem(.separator())
        pauseItem = item("Pause notifications", #selector(togglePause)); menu.addItem(pauseItem)
        menu.addItem(item("Send test notification", #selector(testNotification)))
        permissionItem = item("Notification settings…", #selector(notificationSettings)); menu.addItem(permissionItem)
        menu.addItem(.separator())
        loginItem = item("Launch at login", #selector(toggleLogin)); menu.addItem(loginItem)
        menu.addItem(item("About Pixel Fleet", #selector(about)))
        menu.addItem(item("Quit Pixel Fleet", #selector(quit)))
        statusItem.menu = menu
        center.requestAuthorization(options: [.alert, .sound, .badge]) { _, _ in self.writeStatus() }
        timer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { _ in self.processInbox() }
        workerTimer = Timer.scheduledTimer(withTimeInterval: 30, repeats: true) { _ in self.startWorker() }
        startWorker()
        writeStatus()
    }

    func item(_ title: String, _ action: Selector) -> NSMenuItem {
        let result = NSMenuItem(title: title, action: action, keyEquivalent: "")
        result.target = self
        return result
    }

    var paused: Bool { FileManager.default.fileExists(atPath: pauseFile.path) }
    var blocked: Bool {
        guard let data = try? Data(contentsOf: directory.appendingPathComponent("blocker.json")),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let value = json["until"] as? String else { return false }
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let precise = formatter.date(from: value)
        formatter.formatOptions = [.withInternetDateTime]
        return (precise ?? formatter.date(from: value) ?? .distantPast) > Date()
    }

    func menuWillOpen(_ menu: NSMenu) {
        pauseItem.title = paused ? "Resume notifications" : "Pause notifications"
        statusItem.button?.appearsDisabled = paused
        loginItem.state = SMAppService.mainApp.status == .enabled ? .on : .off
        if SMAppService.mainApp.status == .requiresApproval { loginItem.title = "Launch at login — approval needed…" }
        else { loginItem.title = "Launch at login" }
        writeStatus()
    }

    @objc func togglePause() {
        do {
            if paused { try FileManager.default.removeItem(at: pauseFile) }
            else { try Data("paused".utf8).write(to: pauseFile, options: .atomic) }
            statusItem.button?.appearsDisabled = paused
        } catch { showError(error.localizedDescription) }
    }

    @objc func toggleLogin() {
        do {
            switch SMAppService.mainApp.status {
            case .enabled: try SMAppService.mainApp.unregister()
            case .requiresApproval: SMAppService.openSystemSettingsLoginItems()
            default: try SMAppService.mainApp.register()
            }
            if SMAppService.mainApp.status == .requiresApproval { SMAppService.openSystemSettingsLoginItems() }
            writeStatus()
        } catch { showError("Could not change launch at login: \(error.localizedDescription)") }
    }

    func startWorker() {
        let process = Process()
        process.executableURL = cli
        process.arguments = ["notify", "start"]
        process.environment = environment()
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        try? process.run()
    }

    func environment() -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = FileManager.default.homeDirectoryForCurrentUser.path + "/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
        return env
    }

    func processInbox() {
        guard !paused && !blocked else { return }
        let files = (try? FileManager.default.contentsOfDirectory(at: inbox, includingPropertiesForKeys: nil)) ?? []
        for file in files where file.pathExtension == "json" {
            let id = file.deletingPathExtension().lastPathComponent
            guard !inFlight.contains(id), id.count == 32, id.allSatisfy({ $0.isHexDigit }) else { continue }
            guard let data = try? Data(contentsOf: file), let handoff = try? JSONDecoder().decode(Handoff.self, from: data), handoff.id == id else {
                acknowledge(id, error: "Invalid notification request")
                continue
            }
            inFlight.insert(id)
            submit(handoff) { error in
                DispatchQueue.main.async {
                    self.acknowledge(id, error: error)
                    self.inFlight.remove(id)
                }
            }
        }
    }

    func acknowledge(_ id: String, error: String?) {
        // The caller owns request lifetimes; do not deliver expired requests.
        let file = inbox.appendingPathComponent(id + ".json")
        guard FileManager.default.fileExists(atPath: file.path) else { return }
        try? Data((error ?? "ok").utf8).write(to: inbox.appendingPathComponent(id + ".ack"), options: .atomic)
        try? FileManager.default.removeItem(at: file)
    }

    func submit(_ handoff: Handoff, completion: @escaping (String?) -> Void) {
        center.getNotificationSettings { settings in
            guard settings.authorizationStatus == .authorized || settings.authorizationStatus == .provisional else {
                completion("Allow Pixel Fleet notifications in System Settings → Notifications")
                return
            }
            DispatchQueue.main.async {
                guard !self.paused && !self.blocked else { completion("Notifications are paused or a break is active"); return }
                let content = UNMutableNotificationContent()
                content.title = "Pixel Fleet"
                content.body = handoff.body
                content.sound = .default
                content.categoryIdentifier = handoff.session.isEmpty ? "DASHBOARD" : "SESSION"
                content.userInfo = ["session": handoff.session, "createdAt": handoff.createdAt]
                let request = UNNotificationRequest(identifier: handoff.id, content: content, trigger: nil)
                self.center.add(request) { error in completion(error?.localizedDescription) }
            }
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list, .sound])
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler completionHandler: @escaping () -> Void) {
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier || response.actionIdentifier == "OPEN" else { completionHandler(); return }
        let info = response.notification.request.content.userInfo
        DispatchQueue.main.async {
            self.openSession(info["session"] as? String ?? "", createdAt: info["createdAt"] as? String ?? "")
            completionHandler()
        }
    }

    @objc func openDashboard() { openSession("", createdAt: "") }

    // Terminal opens a generated command file; no AppleScript app or notification
    // identity is involved. cs validates session identity and the break at click time.
    func openSession(_ name: String, createdAt: String) {
        let file = directory.appendingPathComponent("open-\(UUID().uuidString).command")
        func quote(_ value: String) -> String { "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'" }
        let command = "#!/bin/zsh\n/bin/rm -- \"$0\"\nunset TMUX TMUX_PANE\nexport PATH=" + quote(environment()["PATH"]!) + "\nexec " + quote(cli.path) + " focus " + quote(name) + " " + quote(createdAt) + "\n"
        do {
            try Data(command.utf8).write(to: file, options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: file.path)
            NSWorkspace.shared.open([file], withApplicationAt: URL(fileURLWithPath: "/System/Applications/Utilities/Terminal.app"), configuration: NSWorkspace.OpenConfiguration()) { _, error in
                if let error = error { DispatchQueue.main.async { self.showError(error.localizedDescription) } }
            }
        } catch { showError(error.localizedDescription) }
    }

    @objc func testNotification() {
        if paused || blocked { showError("Notifications are paused or a break is active."); return }
        submit(Handoff(id: UUID().uuidString, body: "Pixel Fleet notifications are ready. Open this notification to return to your dashboard.", session: "", createdAt: "")) { error in
            if let error = error { DispatchQueue.main.async { self.showError(error) } }
        }
    }

    @objc func notificationSettings() {
        NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension")!)
    }
    @objc func about() {
        NSApp.orderFrontStandardAboutPanel(options: [.applicationName: "Pixel Fleet", .applicationVersion: "1.0", .credits: NSAttributedString(string: "Your agents, ready when you are.")])
        NSApp.activate(ignoringOtherApps: true)
    }
    @objc func quit() {
        // Quitting explicitly pauses delivery so the watcher cannot relaunch the app.
        try? Data("paused".utf8).write(to: pauseFile, options: .atomic)
        NSApp.terminate(nil)
    }
    func showError(_ message: String) {
        let alert = NSAlert(); alert.messageText = "Pixel Fleet"; alert.informativeText = message
        NSApp.activate(ignoringOtherApps: true); alert.runModal()
    }
    func writeStatus() {
        center.getNotificationSettings { settings in
            DispatchQueue.main.async {
                self.permissionItem.title = settings.authorizationStatus == .denied ? "Notifications disabled — settings…" : "Notification settings…"
            }
            let state: [String: Any] = ["notificationAuthorization": settings.authorizationStatus.rawValue, "alertStyle": settings.alertStyle.rawValue, "loginStatus": SMAppService.mainApp.status.rawValue, "pid": ProcessInfo.processInfo.processIdentifier]
            if let data = try? JSONSerialization.data(withJSONObject: state, options: [.sortedKeys]) {
                try? data.write(to: self.directory.appendingPathComponent("app-status.json"), options: .atomic)
            }
        }
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
