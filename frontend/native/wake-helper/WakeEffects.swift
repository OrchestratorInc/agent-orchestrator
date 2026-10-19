import Foundation
import AppKit
import CoreAudio
import Darwin

struct PausedWakeApp: Codable {
    let pid: Int32
    let started: UInt64
    let bundle: String
    var applied: Bool
}
struct MutedWakeOutput: Codable {
    let uid: String
    let original: Bool
}
struct WakeEffectsJournal: Codable {
    var version = 1
    var paused: [PausedWakeApp] = []
    var audio: MutedWakeOutput? = nil
    var lights: [WakeLightChange]? = nil
    var blocked: [String: String] = [:]
}

// Recovery owns identities, not PIDs or the current default output. Prepared
// SIGSTOP records are deliberately uncertain after a crash and need explicit
// user recovery before SIGCONT; pre-stopped applications are never adopted.
struct WakeEffects {
    let url: URL
    var journal: WakeEffectsJournal
    var reason = ""
    let lights: any WakeLightDriver
    init(url: URL, lights: any WakeLightDriver = WakeLights()) throws {
        self.url = url
        self.lights = lights
        journal = FileManager.default.fileExists(atPath: url.path) ? try read(WakeEffectsJournal.self, url) : WakeEffectsJournal()
        guard journal.version == 1 else { throw NSError(domain: "unsupported effects journal", code: 1) }
    }
    func persist() throws { try write(journal, url) }
    mutating func prune(_ ids: Set<String>) throws {
        let next = journal.blocked.filter { ids.contains($0.key) }
        if next.count != journal.blocked.count { journal.blocked = next; try persist() }
    }
    mutating func block(_ leases: [WakeLease], reason: String) throws {
        for lease in leases { journal.blocked[lease.id] = reason }
        try persist()
    }
    static func process(_ pid: Int32) -> proc_bsdinfo? {
        var info = proc_bsdinfo()
        guard proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, Int32(MemoryLayout<proc_bsdinfo>.size)) == MemoryLayout<proc_bsdinfo>.size,
              info.pbi_uid == getuid() else { return nil }
        return info
    }
    static func guardedProcesses(_ owners: Set<Int32>) -> Set<Int32> {
        var protected = owners.union([getpid(), getppid()])
        let count = proc_listallpids(nil, 0)
        guard count > 0 && count < 65536 else { return protected }
        var pids = [Int32](repeating: 0, count: Int(count) + 128)
        let actual = pids.withUnsafeMutableBytes { proc_listallpids($0.baseAddress, Int32($0.count)) }
        let runtimes: Set<String> = ["ao", "ao-host", "tmux", "claude", "codex", "opencode", "node", "python", "python3", "deno", "bun", "pi"]
        if actual > 0 {
            for pid in pids.prefix(min(Int(actual), pids.count)) {
                guard let info = process(pid) else { continue }
                let name = withUnsafeBytes(of: info.pbi_comm) { String(decoding: $0.prefix { $0 != 0 }, as: UTF8.self) }
                if runtimes.contains(name) { protected.insert(pid) }
            }
        }
        for owner in Array(protected) {
            var pid = owner
            var seen = Set<Int32>()
            while pid > 1 && !seen.contains(pid) {
                seen.insert(pid); protected.insert(pid)
                guard let info = process(pid) else { break }
                pid = Int32(info.pbi_ppid)
            }
        }
        return protected
    }
    static func descendants(_ root: Int32) -> [Int32] {
        let count = proc_listallpids(nil, 0)
        guard count > 0 && count < 65536 else { return [] }
        var pids = [Int32](repeating: 0, count: Int(count) + 128)
        let actual = pids.withUnsafeMutableBytes { proc_listallpids($0.baseAddress, Int32($0.count)) }
        guard actual > 0 else { return [] }
        var children: [Int32: [Int32]] = [:]
        for pid in pids.prefix(min(Int(actual), pids.count)) {
            if let info = process(pid) { children[Int32(info.pbi_ppid), default: []].append(pid) }
        }
        var result: [Int32] = []
        var queue = [root]
        var seen = Set<Int32>()
        while !queue.isEmpty && seen.count < 4096 {
            let pid = queue.removeFirst()
            if seen.contains(pid) { continue }
            seen.insert(pid); result.append(pid); queue.append(contentsOf: children[pid] ?? [])
        }
        return result
    }
    static func protected(_ app: NSRunningApplication, owners: Set<Int32>) -> Bool {
        guard let bundle = app.bundleIdentifier else { return true }
        let protectedPrefixes = ["com.apple.", "com.aoagents.", "com.microsoft.VSCode", "com.todesktop.", "com.google.Chrome", "com.brave.", "org.mozilla.", "com.jetbrains.", "com.googlecode.iterm2", "dev.warp.", "us.zoom.", "com.microsoft.teams", "com.cisco."]
        if protectedPrefixes.contains(where: bundle.hasPrefix) { return true }
        var pid = app.processIdentifier
        var seen = Set<Int32>()
        while pid > 1 && !seen.contains(pid) {
            if owners.contains(pid) || pid == getpid() { return true }
            seen.insert(pid)
            guard let info = process(pid) else { return true }
            pid = Int32(info.pbi_ppid)
        }
        return false
    }
    static func defaultOutput() -> AudioDeviceID? {
        var address = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDefaultOutputDevice, mScope: kAudioObjectPropertyScopeGlobal, mElement: 0)
        var device: AudioDeviceID = 0
        var size = UInt32(MemoryLayout<AudioDeviceID>.size)
        guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &device) == noErr, device != 0 else { return nil }
        return device
    }
    static func outputUID(_ device: AudioDeviceID) -> String? {
        var address = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyDeviceUID, mScope: kAudioObjectPropertyScopeGlobal, mElement: 0)
        var value: Unmanaged<CFString>? = nil
        var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
        guard AudioObjectGetPropertyData(device, &address, 0, nil, &size, &value) == noErr else { return nil }
        return value?.takeRetainedValue() as String?
    }
    static func output(uid: String) -> AudioDeviceID? {
        var address = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDevices, mScope: kAudioObjectPropertyScopeGlobal, mElement: 0)
        var size: UInt32 = 0
        guard AudioObjectGetPropertyDataSize(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size) == noErr else { return nil }
        var devices = [AudioDeviceID](repeating: 0, count: Int(size) / MemoryLayout<AudioDeviceID>.size)
        guard !devices.isEmpty, AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &devices) == noErr else { return nil }
        return devices.first { outputUID($0) == uid }
    }
    static func inputBusy() -> Bool? {
        var address = AudioObjectPropertyAddress(mSelector: kAudioHardwarePropertyDefaultInputDevice, mScope: kAudioObjectPropertyScopeGlobal, mElement: 0)
        var device: AudioDeviceID = 0
        var size = UInt32(MemoryLayout<AudioDeviceID>.size)
        guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, &device) == noErr else { return nil }
        if device == 0 { return false }
        address = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyDeviceIsRunningSomewhere, mScope: kAudioDevicePropertyScopeInput, mElement: 0)
        var running: UInt32 = 0
        guard AudioObjectGetPropertyData(device, &address, 0, nil, &size, &running) == noErr else { return nil }
        return running != 0
    }
    static func mute(_ device: AudioDeviceID) -> Bool? {
        var address = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyMute, mScope: kAudioDevicePropertyScopeOutput, mElement: 0)
        var value: UInt32 = 0
        var size = UInt32(MemoryLayout<UInt32>.size)
        guard AudioObjectGetPropertyData(device, &address, 0, nil, &size, &value) == noErr else { return nil }
        return value != 0
    }
    static func canMute() -> Bool {
        guard let device = defaultOutput(), mute(device) != nil else { return false }
        var address = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyMute, mScope: kAudioDevicePropertyScopeOutput, mElement: 0)
        var settable = DarwinBoolean(false)
        return AudioObjectIsPropertySettable(device, &address, &settable) == noErr && settable.boolValue
    }
    static func setMute(_ device: AudioDeviceID, _ muted: Bool) -> Bool {
        var address = AudioObjectPropertyAddress(mSelector: kAudioDevicePropertyMute, mScope: kAudioDevicePropertyScopeOutput, mElement: 0)
        var value: UInt32 = muted ? 1 : 0
        return AudioObjectSetPropertyData(device, &address, 0, nil, UInt32(MemoryLayout<UInt32>.size), &value) == noErr && mute(device) == muted
    }
    mutating func reconcileLights(_ leases: [WakeLease], force: Bool) throws -> Bool {
        let display = leases.contains { $0.restoreDisplay == true }
        let keyboard = leases.contains { $0.darkenKeyboard == true }
        for record in journal.lights ?? [] {
            let wanted = record.kind == "display" ? display : keyboard
            if wanted && !force { continue }
            guard record.boot == lights.boot else { reason = "light_recovery_required"; return false }
            guard let current = lights.value(record.kind, record.identity) else { reason = "light_unavailable"; return false }
            // Any nonzero value means the user/macOS already changed brightness.
            if current <= 0.001 && !lights.set(record.kind, record.identity, record.original) { reason = "recovery_failed"; return false }
            journal.lights?.removeAll { $0.kind == record.kind && $0.identity == record.identity }; try persist()
        }
        if force { return true }
        for (kind, wanted) in [("display", display), ("keyboard", keyboard)] where wanted {
            let targets = lights.targets(kind)
            guard !targets.isEmpty else { reason = "light_unavailable"; return false }
            for identity in targets where !(journal.lights ?? []).contains(where: { $0.kind == kind && $0.identity == identity }) {
                // Never infer the user's brightness from a lid-closed/dimmed zero.
                guard let original = lights.original(kind, identity) else { reason = "light_sample_required"; return false }
                if journal.lights == nil { journal.lights = [] }
                let record = WakeLightChange(kind: kind, identity: identity, boot: lights.boot, original: original)
                journal.lights?.append(record); try persist()
                guard lights.set(kind, identity, 0) else { reason = "light_unavailable"; return false }
            }
        }
        return true
    }
    mutating func reconcile(_ leases: [WakeLease], owners: Set<Int32>, forceRecovery: Bool = false) -> Bool {
        reason = ""
        let eligible = leases.filter { journal.blocked[$0.id] == nil }
        let requested = Set(eligible.flatMap { $0.pauseApps ?? [] })
        let guardians = requested.isEmpty ? owners : Self.guardedProcesses(owners)
        let inputSafe = requested.isEmpty || Self.inputBusy() == false
        let foreground = NSWorkspace.shared.frontmostApplication?.bundleIdentifier
        let apps = requested.isEmpty ? [] : NSWorkspace.shared.runningApplications
        let protectedBundles = Set(apps.filter { Self.protected($0, owners: guardians) }.compactMap(\.bundleIdentifier))
        let desired = inputSafe ? requested.filter { $0 != foreground && !protectedBundles.contains($0) } : Set<String>()
        if !inputSafe && !requested.isEmpty { reason = "recording_active" }
        do {
            let lightOK = try reconcileLights(eligible, force: forceRecovery)
            // Restore paused processes before releasing lid control can suspend us.
            for entry in journal.paused.reversed() {
                guard processStart(entry.pid) == entry.started, let info = Self.process(entry.pid) else {
                    journal.paused.removeAll { $0.pid == entry.pid && $0.started == entry.started }; try persist(); continue
                }
                if desired.contains(entry.bundle) && !forceRecovery {
                    if info.pbi_status != 4 { reason = "effects_conflict"; return false }
                    continue
                }
                if !entry.applied && info.pbi_status == 4 && !forceRecovery { reason = "effects_recovery_required"; return false }
                if info.pbi_status == 4 && (processStart(entry.pid) != entry.started || kill(entry.pid, SIGCONT) != 0) { reason = "recovery_failed"; return false }
                journal.paused.removeAll { $0.pid == entry.pid && $0.started == entry.started }; try persist()
            }
            let muteWanted = eligible.contains { $0.muteAudio == true }
            if let owned = journal.audio {
                // Never restore a different output or overwrite a detectable user change.
                guard let device = Self.output(uid: owned.uid) else { reason = "audio_output_changed"; return false }
                guard let current = Self.mute(device) else { reason = "recovery_failed"; return false }
                if !current { journal.audio = nil; try persist(); if muteWanted { reason = "effects_conflict"; return false } }
                else if !muteWanted || forceRecovery || Self.defaultOutput().flatMap({ Self.outputUID($0) }) != owned.uid {
                    guard Self.setMute(device, owned.original) else { reason = "recovery_failed"; return false }
                    journal.audio = nil; try persist()
                }
            }
            if !lightOK { return false }
            if forceRecovery { return journal.paused.isEmpty && journal.audio == nil && (journal.lights ?? []).isEmpty }
            if muteWanted && journal.audio == nil {
                guard let device = Self.defaultOutput(), let uid = Self.outputUID(device), let original = Self.mute(device) else { reason = "audio_unavailable"; return false }
                if !original {
                    journal.audio = MutedWakeOutput(uid: uid, original: original); try persist()
                    guard Self.setMute(device, true) else { reason = "audio_unavailable"; return false }
                }
            }
            if desired.isEmpty { return true }
            for app in apps {
                guard let bundle = app.bundleIdentifier, desired.contains(bundle),
                      !Self.protected(app, owners: guardians) else { continue }
                // Freeze only a verified same-user app group. Protected descendants
                // make the whole group ineligible rather than partially freezing it.
                let group = Self.descendants(app.processIdentifier)
                if group.isEmpty || group.contains(where: { guardians.contains($0) }) { continue }
                for pid in group {
                    guard let identity = processStart(pid), let info = Self.process(pid), info.pbi_status != 4,
                          !journal.paused.contains(where: { $0.pid == pid && $0.started == identity }) else { continue }
                    guard journal.paused.count < 128 else { reason = "pause_limit"; return false }
                    journal.paused.append(PausedWakeApp(pid: pid, started: identity, bundle: bundle, applied: false)); try persist()
                    guard processStart(pid) == identity, kill(pid, SIGSTOP) == 0 else { reason = "pause_failed"; return false }
                    let until = ProcessInfo.processInfo.systemUptime + 0.1
                    while Self.process(pid)?.pbi_status != 4 && ProcessInfo.processInfo.systemUptime < until { usleep(1000) }
                    guard Self.process(pid)?.pbi_status == 4 else { reason = "effects_recovery_required"; return false }
                    if let index = journal.paused.firstIndex(where: { $0.pid == pid && $0.started == identity }) { journal.paused[index].applied = true }
                    try persist()
                }
            }
            return true
        } catch { reason = "recovery_failed"; return false }
    }
}
