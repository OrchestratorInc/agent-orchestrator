import Foundation
import IOKit
import IOKit.ps
import IOKit.pwr_mgt
import CoreGraphics
import AppKit
import Darwin

let protocolVersion = 5
let home = FileManager.default.homeDirectoryForCurrentUser
let directory = home.appendingPathComponent(".ao/data/wake")
let leasesDirectory = directory.appendingPathComponent("leases")
let journalURL = directory.appendingPathComponent("journal.json")
let statusURL = directory.appendingPathComponent("status.json")

func processStart(_ pid: Int32) -> UInt64? {
    var info = proc_bsdinfo()
    let size = MemoryLayout<proc_bsdinfo>.size
    guard proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, Int32(size)) == size,
          info.pbi_uid == getuid() else { return nil }
    return info.pbi_start_tvsec * 1_000_000 + info.pbi_start_tvusec
}

func secureDirectory(_ url: URL) throws {
    try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true,
                                           attributes: [.posixPermissions: 0o700])
    var st = stat()
    guard lstat(url.path, &st) == 0, st.st_uid == getuid(),
          (st.st_mode & S_IFMT) == S_IFDIR, (st.st_mode & 0o077) == 0 else {
        throw NSError(domain: "AO awake directory permissions", code: 1)
    }
}

func read<T: Decodable>(_ type: T.Type, _ url: URL) throws -> T {
    let fd = open(url.path, O_RDONLY | O_NOFOLLOW | O_CLOEXEC)
    guard fd >= 0 else { throw NSError(domain: "read", code: Int(errno)) }
    defer { close(fd) }
    var st = stat()
    guard fstat(fd, &st) == 0, st.st_uid == getuid(),
          (st.st_mode & S_IFMT) == S_IFREG, st.st_size <= 65536,
          (st.st_mode & 0o077) == 0 else { throw NSError(domain: "unsafe state", code: 1) }
    return try JSONDecoder().decode(type, from: FileHandle(fileDescriptor: fd, closeOnDealloc: false).readDataToEndOfFile())
}

func write<T: Encodable>(_ value: T, _ url: URL) throws {
    let data = try JSONEncoder().encode(value)
    guard data.count <= 65536 else { throw NSError(domain: "awake state exceeds recovery limit", code: 1) }
    let temporary = url.deletingLastPathComponent().appendingPathComponent(UUID().uuidString)
    let fd = open(temporary.path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600)
    guard fd >= 0 else { throw NSError(domain: "write", code: Int(errno)) }
    defer { close(fd); try? FileManager.default.removeItem(at: temporary) }
    let handle = FileHandle(fileDescriptor: fd, closeOnDealloc: false)
    try handle.write(contentsOf: data)
    guard fsync(fd) == 0, rename(temporary.path, url.path) == 0 else {
        throw NSError(domain: "commit", code: Int(errno))
    }
    let parent = open(url.deletingLastPathComponent().path, O_RDONLY | O_DIRECTORY | O_CLOEXEC)
    if parent >= 0 { _ = fsync(parent); close(parent) }
}

func rootProperty(_ key: String) -> Bool? {
    let service = IOServiceGetMatchingService(mach_port_t(0), IOServiceMatching("IOPMrootDomain"))
    guard service != 0 else { return nil }
    defer { IOObjectRelease(service) }
    return IORegistryEntryCreateCFProperty(service, key as CFString, kCFAllocatorDefault, 0)?.takeRetainedValue() as? Bool
}

// This is a global kernel control, NOT a process-owned assertion. Only the
// launchd coordinator calls it; journal + lease recovery must precede acquisition.
func setLidSleepDisabled(_ disabled: Bool) -> Bool {
    let service = IOServiceGetMatchingService(mach_port_t(0), IOServiceMatching("IOPMrootDomain"))
    guard service != 0 else { return false }
    defer { IOObjectRelease(service) }
    var connection: io_connect_t = 0
    guard IOServiceOpen(service, mach_task_self_, 0, &connection) == KERN_SUCCESS else { return false }
    defer { IOServiceClose(connection) }
    var input: UInt64 = disabled ? 1 : 0
    // kPMSetClamshellSleepState, from the SDK's IOPMLibDefs.h.
    return IOConnectCallScalarMethod(connection, 12, &input, 1, nil, nil) == KERN_SUCCESS
}

func power() -> (Bool?, Int?) {
    guard let snapshot = IOPSCopyPowerSourcesInfo()?.takeRetainedValue(),
          let sources = IOPSCopyPowerSourcesList(snapshot)?.takeRetainedValue() as? [CFTypeRef] else { return (nil, nil) }
    for source in sources {
        guard let info = IOPSGetPowerSourceDescription(snapshot, source)?.takeUnretainedValue() as? [String: Any],
              info[kIOPSTypeKey] as? String == kIOPSInternalBatteryType,
              let state = info[kIOPSPowerSourceStateKey] as? String else { continue }
        let current = info[kIOPSCurrentCapacityKey] as? Int
        let maximum = info[kIOPSMaxCapacityKey] as? Int
        let percent = current.flatMap { current in maximum.flatMap { $0 > 0 ? current * 100 / $0 : nil } }
        return (state == kIOPSBatteryPowerValue, percent)
    }
    // The power sources were readable and none is an internal battery (a desktop
    // Mac), so the machine is on wall power. Treating this as unknown would latch
    // the battery_unknown safety stop forever with no way to resume.
    return (false, nil)
}

func docked() -> Bool? {
    let (battery, _) = power()
    guard let battery else { return nil }
    if battery { return false }
    var displays = [CGDirectDisplayID](repeating: 0, count: 32)
    var count: UInt32 = 0
    guard CGGetOnlineDisplayList(UInt32(displays.count), &displays, &count) == .success else { return nil }
    return displays.prefix(Int(count)).contains { CGDisplayIsBuiltin($0) == 0 }
}

func displaySleep() -> Bool {
    let command = Process()
    command.executableURL = URL(fileURLWithPath: "/usr/bin/pmset")
    command.arguments = ["displaysleepnow"]
    command.standardOutput = FileHandle.nullDevice
    command.standardError = FileHandle.nullDevice
    do { try command.run() } catch { return false }
    // Bound the command; a hanging display request must not stop lease recovery.
    let until = ProcessInfo.processInfo.systemUptime + 2
    while command.isRunning && ProcessInfo.processInfo.systemUptime < until { usleep(10_000) }
    if command.isRunning { kill(command.processIdentifier, SIGKILL) }
    command.waitUntilExit()
    return command.terminationStatus == 0
}

// Process-owned IOPM assertions are automatically released if this helper dies.
struct WakeAssertions {
    var idle: IOPMAssertionID = 0
    var display: IOPMAssertionID = 0
    mutating func update(idle requestedIdle: Bool, display requestedDisplay: Bool) -> Bool {
        if !requestedDisplay && display != 0 { IOPMAssertionRelease(display); display = 0 }
        if !requestedIdle && idle != 0 { IOPMAssertionRelease(idle); idle = 0 }
        if requestedIdle && idle == 0 {
            guard IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleSystemSleep as CFString,
                IOPMAssertionLevel(kIOPMAssertionLevelOn), "AO Stay Awake" as CFString, &idle) == kIOReturnSuccess else { return false }
        }
        if requestedDisplay && display == 0 {
            guard IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleDisplaySleep as CFString,
                IOPMAssertionLevel(kIOPMAssertionLevelOn), "AO Stay Awake display" as CFString, &display) == kIOReturnSuccess else { return false }
        }
        return true
    }
}

// A separate thread kills a stuck coordinator so launchd can replay its
// already-fsynced recovery journals. It never performs OS mutations itself.
final class WakeWatchdog {
    private let lock = NSLock()
    private var last = ProcessInfo.processInfo.systemUptime
    private var running = true
    init() {
        Thread.detachNewThread { [self] in
            while true {
                Thread.sleep(forTimeInterval: 1)
                lock.lock(); let active = running; let age = ProcessInfo.processInfo.systemUptime - last; lock.unlock()
                if !active { return }
                if age > 30 { kill(getpid(), SIGKILL); return }
            }
        }
    }
    func beat() { lock.lock(); last = ProcessInfo.processInfo.systemUptime; lock.unlock() }
    func stop() { lock.lock(); running = false; lock.unlock() }
}

var stopping = false
func serve() throws {
    let watchdog = WakeWatchdog()
    defer { watchdog.stop() }
    signal(SIGTERM) { _ in stopping = true }
    signal(SIGINT) { _ in stopping = true }
    try secureDirectory(directory)
    try secureDirectory(leasesDirectory)
    let lock = open(directory.appendingPathComponent("coordinator.lock").path, O_CREAT | O_RDWR | O_NOFOLLOW | O_CLOEXEC, 0o600)
    guard lock >= 0, flock(lock, LOCK_EX | LOCK_NB) == 0 else { return }
    defer { close(lock) }
    var journal: WakeJournal
    if FileManager.default.fileExists(atPath: journalURL.path) {
        // An unreadable/corrupt journal is never interpreted as "nothing to recover".
        do { journal = try read(WakeJournal.self, journalURL) } catch {
            try? write(WakeStatus(version: protocolVersion, updatedAt: Date().timeIntervalSince1970,
                                  active: false, reason: "recovery_failed", leases: [], blocked: [:]), statusURL)
            throw error
        }
    } else { journal = WakeJournal() }
    var ownership = WakeOwnership(journal: journal)
    var assertions = WakeAssertions()
    func restore() -> Bool {
        ownership.release(persist: { try write($0, journalURL) }, restore: {
            // Clear our override, never manufacture a replacement bit from
            // a display-count heuristic. macOS owns normal docking separately.
            return setLidSleepDisabled(false)
        })
    }
    var effects: WakeEffects
    do { effects = try WakeEffects(url: directory.appendingPathComponent("effects.json")) }
    catch {
        // An unreadable optional journal cannot leave the global lid override
        // armed forever. Preserve the file and report recovery before release.
        try? write(WakeStatus(version: protocolVersion, updatedAt: Date().timeIntervalSince1970,
                              active: false, reason: "recovery_failed", leases: [], blocked: ownership.journal.blocked), statusURL)
        _ = restore()
        throw error
    }
    var lowPower: WakeLowPower
    do { lowPower = try WakeLowPower(url: directory.appendingPathComponent("low-power.json")) }
    catch {
        _ = effects.reconcile([], owners: [])
        _ = restore()
        try? write(WakeStatus(version: protocolVersion, updatedAt: Date().timeIntervalSince1970,
            active: false, reason: "low_power_recovery_failed", leases: [], blocked: ownership.journal.blocked), statusURL)
        throw error
    }
    func reconcilePower(_ leases: [WakeLease], onBattery: Bool?, percent: Int?, thermal: String, force: Bool = false) -> Bool {
        do {
            return try WakePowerCommands.withAuthorizationLock(directory) {
                if WakePowerCommands.revocationPending(directory) {
                    lowPower.reason = "low_power_revocation_pending"
                    return false
                }
                // After revocation, a new owner cannot create an uncertain claim
                // for a command that authorization will necessarily reject.
                if lowPower.journal.claims.isEmpty && leases.contains(where: { ($0.lowPowerBatteryPercent ?? 0) > 0 || $0.lowPowerOnSeriousHeat == true }) && !WakePowerCommands.authorizationInstalled() {
                    lowPower.reason = "low_power_authorization_required"
                    return false
                }
                return lowPower.reconcile(leases, onBattery: onBattery, percent: percent, thermal: thermal, force: force)
            }
        } catch {
            // Keep lid/thermal monitoring running during a macOS approval dialog.
            lowPower.reason = "low_power_authorization_in_progress"
            return false
        }
    }
    defer {
        let powerClean = reconcilePower([], onBattery: nil, percent: nil, thermal: "unknown")
        _ = assertions.update(idle: false, display: false)
        _ = effects.reconcile([], owners: [])
        let clean = restore()
        try? write(WakeStatus(version: protocolVersion, updatedAt: Date().timeIntervalSince1970,
                              active: false, reason: !powerClean ? lowPower.reason : clean ? "helper_unavailable" : "recovery_failed",
                              leases: [], blocked: ownership.journal.blocked), statusURL)
    }
    var policy = WakePolicy()
    var displayPolicy = WakeDisplayPolicy()
    func requests() throws -> [WakeLease] {
        let files = try FileManager.default.contentsOfDirectory(at: leasesDirectory, includingPropertiesForKeys: nil)
        return files.filter { $0.pathExtension == "json" }.compactMap { try? read(WakeLease.self, $0) }
    }
    let (initialBattery, initialPercent) = power()
    let initialReason = !platformSupported ? "unsupported" : WakePolicy.safety(
        thermalCritical: ProcessInfo.processInfo.thermalState == .critical,
        onBattery: initialBattery, percent: initialPercent)
    let initialLeases = try policy.live(requests(), now: ProcessInfo.processInfo.systemUptime,
                                        wall: Date().timeIntervalSince1970, identity: processStart)
        .filter { ownership.journal.blocked[$0.id] == nil }
    // Persist a safety stop before restoring: restoration may immediately sleep
    // the Mac, and a quick wake must not silently clear that stop.
    try ownership.latch(initialLeases, reason: initialReason, persist: { try write($0, journalURL) })
    for lease in initialLeases {
        try ownership.latch([lease], reason: WakePolicy.safety(
            thermalCritical: ProcessInfo.processInfo.thermalState == .critical,
            onBattery: initialBattery, percent: initialPercent, minimumPercent: lease.stopBatteryPercent ?? 10),
            persist: { try write($0, journalURL) })
    }
    // Reconcile before serving. A helper restart must not clear a valid live
    // owner's bit with the lid shut: clearing it can immediately sleep the Mac.
    // Abandoned, unsafe, expired and reused-PID requests still restore first.
    let initialAccepted = initialLeases.filter { ownership.journal.blocked[$0.id] == nil }
    if initialAccepted.isEmpty { _ = effects.reconcile([], owners: []) }
    if ownership.journal.owned && (initialReason != nil || initialAccepted.isEmpty) && !restore() {
        try write(WakeStatus(version: protocolVersion, updatedAt: Date().timeIntervalSince1970,
                             active: false, reason: "recovery_failed", leases: [], blocked: ownership.journal.blocked), statusURL)
        throw NSError(domain: "recovery", code: 1)
    }
    while !stopping {
        watchdog.beat()
        let now = Date().timeIntervalSince1970
        let uptime = ProcessInfo.processInfo.systemUptime
        let requests = try requests()
        let live = policy.live(requests, now: uptime, wall: now, identity: processStart)
        let existing = Set(requests.map(\.id))
        ownership.journal.blocked = ownership.journal.blocked.filter { existing.contains($0.key) }
        let (onBattery, percent) = power()
        let safetyReason = !platformSupported ? "unsupported" : WakePolicy.safety(
            thermalCritical: ProcessInfo.processInfo.thermalState == .critical,
            onBattery: onBattery, percent: percent)
        try ownership.latch(live, reason: safetyReason, persist: { try write($0, journalURL) })
        for lease in live {
            try ownership.latch([lease], reason: WakePolicy.safety(
                thermalCritical: ProcessInfo.processInfo.thermalState == .critical,
                onBattery: onBattery, percent: percent, minimumPercent: lease.stopBatteryPercent ?? 10),
                persist: { try write($0, journalURL) })
        }
        let accepted = live.filter { ownership.journal.blocked[$0.id] == nil }
        if rootProperty("AppleClamshellState") == false && accepted.contains(where: { $0.restoreDisplay == true || $0.darkenKeyboard == true }) { effects.lights.sampleOpen() }
        try effects.prune(Set(live.map(\.id)))
        let effectsWanted = rootProperty("AppleClamshellState") == true && docked() == false
            ? accepted.filter { $0.wantsEffects } : []
        struct RecoveryRequest: Codable { let id: String; let writtenAt: Double }
        let recoveryURL = directory.appendingPathComponent("effects-recovery.json")
        let recovery = try? read(RecoveryRequest.self, recoveryURL)
        let forceRecovery = recovery.map { UUID(uuidString: $0.id) != nil && now - $0.writtenAt >= -5 && now - $0.writtenAt < 30 } ?? false
        let effectOK = effects.reconcile(effectsWanted, owners: Set(live.map(\.pid)), forceRecovery: forceRecovery && effectsWanted.isEmpty)
        let thermal: String
        switch ProcessInfo.processInfo.thermalState {
        case .nominal: thermal = "nominal"
        case .fair: thermal = "fair"
        case .serious: thermal = "serious"
        case .critical: thermal = "critical"
        @unknown default: thermal = "unknown"
        }
        let powerOK = reconcilePower(accepted, onBattery: onBattery, percent: percent, thermal: thermal,
            force: forceRecovery && !accepted.contains { ($0.lowPowerBatteryPercent ?? 0) > 0 || $0.lowPowerOnSeriousHeat == true })
        if forceRecovery && effectsWanted.isEmpty && effectOK && powerOK { try? FileManager.default.removeItem(at: recoveryURL) }
        if !effectOK {
            try effects.block(effectsWanted, reason: effects.reason)
        }
        let effectReason = effects.reason.isEmpty ? (effects.journal.blocked.values.first ?? "") : effects.reason
        var reason = ""
        var active = false
        let lidClosed = rootProperty("AppleClamshellState") == true
        var idleActive = !accepted.isEmpty && assertions.update(idle: true,
            display: accepted.contains { $0.displayAwake == true } && !lidClosed)
        if !idleActive { _ = assertions.update(idle: false, display: false) }
        let lidLeases = idleActive ? accepted.filter { $0.wantsLid } : []
        if !accepted.isEmpty && !idleActive { reason = "native_failed" }
        if lidLeases.isEmpty {
            if !restore() { reason = "recovery_failed" }
        } else if rootProperty("AppleClamshellState") != nil {
            if ownership.journal.owned {
                active = setLidSleepDisabled(true)
            } else {
                active = try ownership.acquire(persist: { try write($0, journalURL) },
                                               disable: { setLidSleepDisabled(true) })
            }
            // The registry's causes-sleep property updates on lid/power
            // events, not synchronously with this command. An accepted
            // native command arms the control; don't falsely fail an
            // open-lid acquisition by treating that property as an ACK.
            if !active { reason = "native_failed" }
        } else { reason = "native_failed" }
        let closedUndocked = active && rootProperty("AppleClamshellState") == true && docked() != true
        if displayPolicy.needsSleep(closedUndocked: closedUndocked) && !displaySleep() {
            for lease in accepted { ownership.journal.blocked[lease.id] = "display_failed" }
            try write(ownership.journal, journalURL)
            active = false
            reason = restore() ? "display_failed" : "recovery_failed"
        }
        if !active && ownership.journal.owned && reason != "recovery_failed" {
            if !restore() { reason = "recovery_failed" }
        }
        if reason == "display_failed" || reason == "recovery_failed" {
            _ = assertions.update(idle: false, display: false)
            idleActive = false
        }
        if reason.isEmpty { reason = lowPower.reason.isEmpty ? effectReason : lowPower.reason }
        try write(WakeStatus(version: protocolVersion, updatedAt: now, active: idleActive, reason: reason,
                             leases: idleActive ? accepted.map(\.id) : [], blocked: ownership.journal.blocked, lidActive: active, effectsBlocked: effects.journal.blocked), statusURL)
        sleep(2)
    }
}

let platformSupported: Bool = {
#if arch(arm64)
    return WakePolicy.supportsOS(majorVersion: ProcessInfo.processInfo.operatingSystemVersion.majorVersion)
#else
    return false
#endif
}()
let supported = platformSupported && rootProperty("AppleClamshellState") != nil && power().0 != nil
if CommandLine.arguments == [CommandLine.arguments[0], "probe"] {
    let object: [String: Any] = ["supported": supported, "started": processStart(getppid()) ?? 0, "version": protocolVersion]
    if let data = try? JSONSerialization.data(withJSONObject: object) { FileHandle.standardOutput.write(data) }
} else if CommandLine.arguments == [CommandLine.arguments[0], "observe"] {
    let (battery, percent) = power()
    var displays = [CGDirectDisplayID](repeating: 0, count: 32)
    var count: UInt32 = 0
    let displayOK = CGGetOnlineDisplayList(UInt32(displays.count), &displays, &count) == .success
    let thermal: String
    switch ProcessInfo.processInfo.thermalState {
    case .nominal: thermal = "nominal"
    case .fair: thermal = "fair"
    case .serious: thermal = "serious"
    case .critical: thermal = "critical"
    @unknown default: thermal = "unknown"
    }
    let guardians = WakeEffects.guardedProcesses([getppid()])
    let apps: [[String: Any]] = NSWorkspace.shared.runningApplications.compactMap { app in
        // Only apps with a Dock presence: background agents and system services
        // (loginwindow, WindowManager, ...) mean nothing to users here.
        guard app.activationPolicy == .regular, let id = app.bundleIdentifier, let identity = processStart(app.processIdentifier) else { return nil }
        return ["id": id, "name": app.localizedName ?? id, "identity": String(app.processIdentifier) + ":" + String(identity), "protected": WakeEffects.protected(app, owners: guardians)]
    }
    var lowPower = false
    if #available(macOS 12.0, *) { lowPower = ProcessInfo.processInfo.isLowPowerModeEnabled }
    var object: [String: Any] = ["thermal": thermal, "uptimeSeconds": ProcessInfo.processInfo.systemUptime,
        "externalDisplays": displayOK ? displays.prefix(Int(count)).filter { CGDisplayIsBuiltin($0) == 0 }.count : -1,
        "lowPowerMode": lowPower, "lowPowerControl": supported && WakePowerCommands.parse(WakePowerCommands.run("/usr/bin/pmset", ["-g", "custom"]).1).count == 2,
        "lowPowerAuthorized": supported && WakePowerCommands.authorized(), "applications": apps,
        "displayControl": WakeLights().available("display"), "keyboardControl": WakeLights().available("keyboard"), "audioControl": WakeEffects.canMute(), "appPause": WakeEffects.inputBusy() == false,
        "knownNetworks": wakeKnownNetworks(),
        "browsers": wakeBrowserTargets.filter { trustedBrowser($0) != nil }.map { ["id": $0.id, "name": $0.name, "identity": "", "protected": false] }]
    if let connected = wakeNetworkConnected() { object["networkConnected"] = connected }
    if let battery { object["onBattery"] = battery }
    if let percent { object["batteryPercent"] = percent }
    if let data = try? JSONSerialization.data(withJSONObject: object) { FileHandle.standardOutput.write(data) }
} else if CommandLine.arguments == [CommandLine.arguments[0], "authorize-low-power"] || CommandLine.arguments == [CommandLine.arguments[0], "revoke-low-power"] {
    let revoking = CommandLine.arguments[1] == "revoke-low-power"
    var ok = false
    var detail = "Low Power Mode authorization is unavailable on this Mac."
    if supported && getuid() > 0 {
        do {
            try secureDirectory(directory)
            if revoking {
                // Recovery must run before taking the authorization lock. While
                // the dialog is open, the coordinator cannot acquire new claims.
                let until = ProcessInfo.processInfo.systemUptime + 10
                repeat {
                    do {
                        try WakePowerCommands.withAuthorizationLock(directory) {
                            let wall = Date().timeIntervalSince1970
                            let status = try read(WakeStatus.self, statusURL)
                            guard status.version == protocolVersion && abs(wall - status.updatedAt) < 5 else { throw NSError(domain: "helper upgrade required", code: 1) }
                            let files = try FileManager.default.contentsOfDirectory(at: leasesDirectory, includingPropertiesForKeys: nil)
                            let leases = files.filter { $0.pathExtension == "json" }.compactMap { try? read(WakeLease.self, $0) }
                            var policy = WakePolicy()
                            let live = policy.live(leases, now: ProcessInfo.processInfo.systemUptime, wall: wall, identity: processStart)
                            let journal = try WakeLowPower(url: directory.appendingPathComponent("low-power.json")).journal
                            guard mayRevokeLowPower(live, journal) else { throw NSError(domain: "power recovery pending", code: 1) }
                            // Persist a barrier before launching the prompt. If this
                            // process dies, no new power claim may race a late removal.
                            try write(true, WakePowerCommands.revocationMarker(directory))
                            let result = WakePowerCommands.run("/usr/bin/osascript", ["-e", WakePowerCommands.revocationScript(getuid())], timeout: 110)
                            var state = stat()
                            ok = result.0 == 0 && lstat(WakePowerCommands.rulePath(getuid()), &state) != 0 && errno == ENOENT
                            detail = ok ? "" : "macOS revocation was canceled or removal could not be verified. Automation remains disabled."
                            if result.0 == 0 || result.0 == 1 { _ = unlink(WakePowerCommands.revocationMarker(directory).path) }
                            if WakePowerCommands.revocationPending(directory) {
                                ok = false
                                detail = "Revocation was interrupted or cleanup remains pending. Run Revoke authorization again before authorizing Low Power Mode. Automation remains disabled."
                            }
                        }
                        break
                    } catch {
                        detail = "Disable Low Power Mode in other AO instances and resolve pending power recovery before revoking authorization. An older helper must be upgraded first."
                        usleep(100_000)
                    }
                } while ProcessInfo.processInfo.systemUptime < until
            } else {
                try WakePowerCommands.withAuthorizationLock(directory) {
                    if WakePowerCommands.revocationPending(directory) {
                        detail = "Complete Revoke authorization again before authorizing Low Power Mode."
                        return
                    }
                    let result = WakePowerCommands.run("/usr/bin/osascript", ["-e", WakePowerCommands.authorizationScript(getuid())], timeout: 110)
                    ok = result.0 == 0 && WakePowerCommands.authorized()
                    detail = ok ? "" : "macOS authorization was canceled or could not be verified. Review permissions and retry."
                }
            }
        } catch { detail = "Low Power Mode authorization is already running or its state directory is unavailable." }
    }
    let output = try! JSONSerialization.data(withJSONObject: ["ok": ok, "error": detail])
    FileHandle.standardOutput.write(output)
} else if CommandLine.arguments == [CommandLine.arguments[0], "join-known-network"] {
    struct Request: Decodable { let ssid: String }
    do {
        let request = try JSONDecoder().decode(Request.self, from: FileHandle.standardInput.readData(ofLength: 4096))
        try joinWakeKnownNetwork(request.ssid)
        FileHandle.standardOutput.write(Data("{\"ok\":true}".utf8))
    } catch {
        if let data = try? JSONSerialization.data(withJSONObject: ["ok": false, "error": (error as NSError).domain] as [String: Any]) { FileHandle.standardOutput.write(data) }
        exit(1)
    }
} else if CommandLine.arguments == [CommandLine.arguments[0], "relaunch-browser"] {
    struct Request: Decodable { let browserID: String }
    do {
        let data = FileHandle.standardInput.readData(ofLength: 4096)
        let request = try JSONDecoder().decode(Request.self, from: data)
        try relaunchWakeBrowser(request.browserID)
        FileHandle.standardOutput.write(Data("{\"ok\":true}".utf8))
    } catch {
        let object: [String: Any] = ["ok": false, "error": (error as NSError).domain]
        if let data = try? JSONSerialization.data(withJSONObject: object) { FileHandle.standardOutput.write(data) }
        exit(1)
    }
} else if CommandLine.arguments == [CommandLine.arguments[0], "serve"] {
    do { try serve() } catch { fputs("AO wake helper stopped; recovery will retry.\n", stderr); exit(1) }
} else { exit(2) }
