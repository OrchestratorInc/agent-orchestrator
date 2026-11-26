import AppKit
import Darwin
import CoreGraphics

func processAlive(_ pid: Int32) -> Bool {
    kill(pid, 0) == 0 || errno == EPERM
}

func readJSON<T: Decodable>(_ type: T.Type, at url: URL) -> T? {
    guard let data = try? Data(contentsOf: url), data.count <= 16_384 else { return nil }
    return try? JSONDecoder().decode(type, from: data)
}

final class ProgressController: NSObject, NSApplicationDelegate, NSWindowDelegate {
    let attempt: URL
    let request: UpdateRequest
    var state: UpdateProgressState
    var window: NSWindow!
    var timer: Timer?
    var displayedStage: UpdateStage?
    var windowPresented = false
    // Views are created only once AppKit starts, which happens only when a
    // stage needs the window (see the bottom of this file).
    lazy var title = NSTextField(labelWithString: "")
    lazy var detail = NSTextField(wrappingLabelWithString: "")
    lazy var spinner = NSProgressIndicator()
    lazy var recovery = NSStackView()

    init(attempt: URL, request: UpdateRequest) {
        self.attempt = attempt
        self.request = request
        // A copy started by detachForWindow() keeps the original's stall clock.
        let since = ProcessInfo.processInfo.environment[detachedSinceKey].flatMap(Double.init)
        self.state = UpdateProgressState(request: request, now: since ?? Date().timeIntervalSince1970 * 1000)
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Start as an accessory: no Dock icon and no window on launch. A normal
        // update just closes AO and reopens it, with no second window in the way.
        NSApp.setActivationPolicy(.accessory)
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 500, height: 270),
                          styleMask: [.titled, .closable], backing: .buffered, defer: false)
        window.title = "Updating Orchestrator"
        window.isReleasedWhenClosed = false
        window.delegate = self
        title.font = .systemFont(ofSize: 22, weight: .semibold)
        detail.font = .systemFont(ofSize: 14)
        detail.textColor = .secondaryLabelColor
        detail.preferredMaxLayoutWidth = 430
        spinner.style = .spinning
        spinner.controlSize = .regular
        spinner.isIndeterminate = true
        let version = NSTextField(labelWithString: "Updating to \(request.version)")
        version.font = .systemFont(ofSize: 12)
        version.textColor = .secondaryLabelColor
        recovery.orientation = .horizontal
        recovery.spacing = 10
        recovery.addArrangedSubview(NSButton(title: "Keep Waiting", target: self, action: #selector(keepWaiting)))
        recovery.addArrangedSubview(NSButton(title: "Download Latest App", target: self, action: #selector(download)))
        recovery.addArrangedSubview(NSButton(title: "Close", target: self, action: #selector(closeWindow)))
        let stack = NSStackView(views: [spinner, title, detail, version, recovery])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 16
        stack.translatesAutoresizingMaskIntoConstraints = false
        window.contentView!.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: window.contentView!.leadingAnchor, constant: 32),
            stack.trailingAnchor.constraint(equalTo: window.contentView!.trailingAnchor, constant: -32),
            stack.centerYAnchor.constraint(equalTo: window.contentView!.centerYAnchor),
        ])
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { [weak self] _ in self?.refresh() }
    }

    func legacyEvidence(now: Double) -> LegacyLaunchEvidence? {
        guard !processAlive(request.parentPID) else { return nil }
        let markerURL = attempt.deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("app-state.json")
        guard let marker = readJSON(LegacyAppMarker.self, at: markerURL), marker.matchesLegacyLaunch(request, now: now) else { return nil }
        guard let app = NSWorkspace.shared.runningApplications.first(where: {
            $0.processIdentifier != request.parentPID && !$0.isTerminated && $0.isFinishedLaunching &&
            $0.bundleURL?.standardizedFileURL.path == request.appPath
        }) else { return nil }
        // Public window metadata only. Do not read window titles, capture pixels,
        // request Screen Recording/Accessibility permission, or activate the app.
        let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]]
        let visible = windows?.contains(where: { info in
            guard (info[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == app.processIdentifier,
                  (info[kCGWindowLayer as String] as? NSNumber)?.intValue == 0,
                  (info[kCGWindowAlpha as String] as? NSNumber)?.doubleValue ?? 0 > 0,
                  let bounds = info[kCGWindowBounds as String] as? [String: Any],
                  (bounds["Width"] as? NSNumber)?.doubleValue ?? 0 >= 100,
                  (bounds["Height"] as? NSNumber)?.doubleValue ?? 0 >= 80 else { return false }
            return true
        }) ?? false
        return LegacyLaunchEvidence(marker: marker, pid: app.processIdentifier,
                                    appPath: app.bundleURL?.standardizedFileURL.path,
                                    finishedLaunching: app.isFinishedLaunching, visibleWindow: visible)
    }

    // Bring the window on screen. Called only on the stall/failure paths
    // (.recovery/.reopened) that need attention, never on the normal
    // close-and-reopen path, so a routine update stays silent. This is
    // presentation only: it does not touch the READY handshake or termination,
    // which still key off complete.json / the parent PID (see refresh/cleanup).
    func presentWindow() {
        guard !windowPresented else { return }
        windowPresented = true
        NSApp.setActivationPolicy(.regular)
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func currentStage() -> UpdateStage {
        let completion = readJSON(UpdateCompletion.self, at: attempt.appendingPathComponent("complete.json"))
        let failure = readJSON(UpdateFailure.self, at: attempt.appendingPathComponent("error.json"))
        let now = Date().timeIntervalSince1970 * 1000
        if failure != nil { state.noteFailure(now: now) }
        return state.stage(now: now,
                           parentAlive: processAlive(request.parentPID), completion: completion, failure: failure?.message, legacy: legacyEvidence(now: now))
    }

    func refresh() {
        let next = currentStage()
        if next == .complete || next == .legacyComplete || next == .abandoned {
            cleanup()
            NSApp.terminate(nil)
            return
        }
        guard next != displayedStage else { return }
        displayedStage = next
        recovery.arrangedSubviews.forEach { $0.isHidden = false }
        // Single seam for whether this stage is user-facing. The healthy
        // close-and-reopen stages return false and stay silent; only the
        // stall/failure stages present. Keeping the decision on the stage means
        // StateTests can assert it without driving AppKit.
        if next.presentsWindow { presentWindow() }
        switch next {
        case .closing:
            // Healthy path: stay an accessory with no window. The close and
            // reopen happen silently. The title/detail are still primed in case
            // the stage later escalates to .recovery and the window comes up.
            title.stringValue = "Closing Orchestrator"
            detail.stringValue = "Preparing to install your update. This window will stay open while Orchestrator restarts."
            recovery.isHidden = true
            spinner.startAnimation(nil)
        case .installing:
            // Healthy path: also silent, no window. See .closing above.
            title.stringValue = "Installing and reopening Orchestrator"
            detail.stringValue = "macOS is installing the update. Orchestrator will reopen automatically when it is ready."
            recovery.isHidden = true
            spinner.startAnimation(nil)
        case .recovery(let message):
            window.setContentSize(NSSize(width: 500, height: 340))
            title.stringValue = "Still waiting for Orchestrator"
            detail.stringValue = String(message.prefix(260))
            recovery.isHidden = false
            spinner.stopAnimation(nil)
        case .reopened:
            title.stringValue = "Orchestrator has reopened"
            detail.stringValue = "This version of Orchestrator cannot confirm when its window is ready. You can close this progress window."
            recovery.isHidden = false
            recovery.arrangedSubviews.prefix(2).forEach { $0.isHidden = true }
            spinner.stopAnimation(nil)
        case .complete, .legacyComplete, .abandoned: break
        }
    }

    @objc func keepWaiting() {
        state.keepWaiting(now: Date().timeIntervalSince1970 * 1000)
        refresh()
    }
    @objc func download() {
        NSWorkspace.shared.open(URL(string: "https://github.com/Untrivial-ai/agent-orchestrator/releases/latest")!)
    }
    @objc func closeWindow() { window.performClose(nil) }
    func windowWillClose(_ notification: Notification) {
        if !processAlive(request.parentPID) { cleanup() }
        NSApp.terminate(nil)
    }
    func cleanup() {
        timer?.invalidate()
        // The executable and its data have a dedicated directory. Never touch active.json:
        // a newer attempt may already own that pointer.
        try? FileManager.default.removeItem(at: attempt)
    }
}

let detachedSinceKey = "AO_UPDATE_PROGRESS_SINCE"

// Start a copy of this helper and let this process exit, so the copy is
// orphaned (its parent becomes launchd) before it starts AppKit. macOS files an
// AppKit process under the app that is its parent, so a window shown while AO
// is still our parent would be counted as a running AO, and ShipIt cancels the
// install when AO finally quits. Returns false if the copy could not start.
func detachForWindow(executable: URL, attempt: URL, since: Double) -> Bool {
    var actions: posix_spawn_file_actions_t?
    posix_spawn_file_actions_init(&actions)
    defer { posix_spawn_file_actions_destroy(&actions) }
    // AO stopped reading our stdout after READY; the copy must not write to it.
    posix_spawn_file_actions_addopen(&actions, 1, "/dev/null", O_WRONLY, 0)
    var environment = ProcessInfo.processInfo.environment
    environment[detachedSinceKey] = String(since)
    let argv: [UnsafeMutablePointer<CChar>?] = [strdup(executable.path), strdup(attempt.path), nil]
    let envp: [UnsafeMutablePointer<CChar>?] = environment.map { strdup("\($0.key)=\($0.value)") } + [nil]
    defer { (argv + envp).forEach { free($0) } }
    var pid: pid_t = 0
    return posix_spawn(&pid, executable.path, &actions, nil, argv, envp) == 0
}

// Only a copied helper, outside any .app, may run. Refuse an arbitrary cleanup path.
guard CommandLine.arguments.count == 2 else { exit(2) }
let attempt = URL(fileURLWithPath: CommandLine.arguments[1]).standardizedFileURL
let executable = URL(fileURLWithPath: CommandLine.arguments[0]).standardizedFileURL.resolvingSymlinksInPath()
guard attempt == attempt.resolvingSymlinksInPath(),
      attempt.deletingLastPathComponent().lastPathComponent == "update-restart",
      attempt.lastPathComponent.hasPrefix("attempt-"),
      !attempt.pathComponents.contains(where: { $0.hasSuffix(".app") }),
      executable == attempt.appendingPathComponent("ao-update-progress"),
      let request = readJSON(UpdateRequest.self, at: attempt.appendingPathComponent("request.json")), request.valid else { exit(2) }
let controller = ProgressController(attempt: attempt, request: request)
let isDetachedCopy = ProcessInfo.processInfo.environment[detachedSinceKey] != nil
var detachTried = false
// READY is the handshake the parent waits for before it lets Squirrel quit AO.
FileHandle.standardOutput.write(Data("READY\n".utf8))
// Stay a plain process, not an AppKit app, until a stage needs the window.
// macOS files an AppKit process whose parent is AO under AO's own bundle
// identifier (an embedded Info.plist does not change that), and
// Squirrel's ShipIt cancels the install while any app with that identifier is
// running ("App Still Running Error"). Starting NSApplication here made this
// helper cancel the very update it was reporting on (#6911). The window is
// only needed on the stall and failure paths.
while true {
    let next = controller.currentStage()
    if next == .complete || next == .legacyComplete || next == .abandoned {
        controller.cleanup()
        exit(0)
    }
    if next.presentsWindow {
        if getppid() == 1 { break }
        // AO is still our parent (its quit has stalled for three minutes):
        // hand the window to an orphaned copy. If that copy can't start, or
        // this is the copy and its starter hasn't exited yet, stay headless
        // until launchd adopts us rather than start AppKit as AO's child.
        if !isDetachedCopy && !detachTried {
            detachTried = true
            if detachForWindow(executable: executable, attempt: attempt, since: controller.state.waitingSince) { exit(0) }
        }
    }
    Thread.sleep(forTimeInterval: isDetachedCopy ? 0.05 : 0.5)
}
NSApplication.shared.delegate = controller
NSApplication.shared.run()
