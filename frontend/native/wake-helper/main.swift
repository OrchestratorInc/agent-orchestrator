import Foundation
import IOKit
import IOKit.ps
import IOKit.pwr_mgt
import CoreGraphics
import Darwin

let protocolVersion = 2
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
    return (nil, nil)
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

var stopping = false
func serve() throws {
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
    func restore() -> Bool {
        ownership.release(persist: { try write($0, journalURL) }, restore: {
            // Clear our override, never manufacture a replacement bit from
            // a display-count heuristic. macOS owns normal docking separately.
            return setLidSleepDisabled(false)
        })
    }
    defer {
        let clean = restore()
        try? write(WakeStatus(version: protocolVersion, updatedAt: Date().timeIntervalSince1970,
                              active: false, reason: clean ? "helper_unavailable" : "recovery_failed",
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
    // Reconcile before serving. A helper restart must not clear a valid live
    // owner's bit with the lid shut: clearing it can immediately sleep the Mac.
    // Abandoned, unsafe, expired and reused-PID requests still restore first.
    if ownership.journal.owned && (initialReason != nil || initialLeases.isEmpty) && !restore() {
        try write(WakeStatus(version: protocolVersion, updatedAt: Date().timeIntervalSince1970,
                             active: false, reason: "recovery_failed", leases: [], blocked: ownership.journal.blocked), statusURL)
        throw NSError(domain: "recovery", code: 1)
    }
    while !stopping {
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
        let accepted = live.filter { ownership.journal.blocked[$0.id] == nil }
        var reason = ""
        var active = false
        if accepted.isEmpty {
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
        try write(WakeStatus(version: protocolVersion, updatedAt: now, active: active, reason: reason,
                             leases: active ? accepted.map(\.id) : [], blocked: ownership.journal.blocked), statusURL)
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
} else if CommandLine.arguments == [CommandLine.arguments[0], "serve"] {
    do { try serve() } catch { fputs("AO wake helper stopped; recovery will retry.\n", stderr); exit(1) }
} else { exit(2) }
