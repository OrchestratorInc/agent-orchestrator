import Foundation
import Darwin

// Only fixed Apple executables and four exact pmset operations cross the
// authorization boundary. No AO executable or user-supplied command runs as root.
enum WakePowerCommands {
    static let profiles = ["battery", "charger"]
    static func flag(_ profile: String) -> String? {
        switch profile { case "battery": return "-b"; case "charger": return "-c"; default: return nil }
    }
    static func rule(_ uid: UInt32) -> String {
        let commands = ["-b", "-c"].flatMap { flag in [0, 1].map { "/usr/bin/pmset \(flag) lowpowermode \($0)" } }
        return "# AO Low Power Mode: fixed operations only\n" + commands.map { "#\(uid) ALL=(root) TIMEOUT=5s NOPASSWD: " + $0 }.joined(separator: "\n") + "\n"
    }
    static func revocationMarker(_ directory: URL) -> URL { directory.appendingPathComponent("power-revocation.json") }
    static func revocationPending(_ directory: URL) -> Bool {
        var state = stat()
        return lstat(revocationMarker(directory).path, &state) == 0 || errno != ENOENT
    }
    static func rulePath(_ uid: UInt32) -> String { "/etc/sudoers.d/ao-low-power-\(uid)" }
    static func privilegedScript(_ shell: String) -> String {
        let literal = shell.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"").replacingOccurrences(of: "\n", with: "\\n")
        return "do shell script \"\(literal)\" with administrator privileges"
    }
    static func revocationScript(_ uid: UInt32) -> String {
        privilegedScript("/bin/rm -f \(rulePath(uid))")
    }
    static func withAuthorizationLock<T>(_ directory: URL, _ action: () throws -> T) throws -> T {
        let fd = open(directory.appendingPathComponent("power-authorization.lock").path, O_CREAT | O_RDWR | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else { throw NSError(domain: "authorization lock", code: 1) }
        defer { close(fd) }
        guard flock(fd, LOCK_EX | LOCK_NB) == 0 else { throw NSError(domain: "authorization in progress", code: 1) }
        return try action()
    }
    static func authorizationScript(_ uid: UInt32) -> String {
        // uid is numeric, the rule is generated entirely from constants.
        let content = rule(uid).replacingOccurrences(of: "'", with: "'\\''")
        let shell = "set -eu; d=$(/usr/bin/mktemp -d /private/tmp/ao-low-power.XXXXXX); trap '/bin/rm -rf \"$d\"' EXIT; /usr/bin/printf '%s' '\(content)' > \"$d/rule\"; /usr/sbin/visudo -c -f \"$d/rule\" >/dev/null; /usr/bin/install -S -o root -g wheel -m 0440 \"$d/rule\" \(rulePath(uid))"
        return privilegedScript(shell)
    }
    // Reject unreadable and High Power settings. Do not interpret them as off.
    static func parse(_ text: String) -> [String: Bool] {
        var profile = ""
        var values: [String: [String: String]] = [:]
        for raw in text.split(whereSeparator: \.isNewline) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasSuffix(":") {
                profile = line == "Battery Power:" ? "battery" : line == "AC Power:" ? "charger" : ""
            } else if !profile.isEmpty {
                let parts = line.split(whereSeparator: \.isWhitespace)
                if let key = parts.first, key == "lowpowermode" || key == "powermode" {
                    values[profile, default: [:]][String(key)] = parts.count > 1 ? String(parts[1]) : ""
                }
            }
        }
        var result: [String: Bool] = [:]
        for profile in profiles {
            if let value = values[profile]?["lowpowermode"] ?? values[profile]?["powermode"], value == "0" || value == "1" {
                result[profile] = value == "1"
            }
        }
        return result
    }
    static func run(_ executable: String, _ arguments: [String], timeout: Double = 3) -> (Int32, String) {
        let process = Process(); let pipe = Pipe()
        process.executableURL = URL(fileURLWithPath: executable); process.arguments = arguments
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = pipe; process.standardError = FileHandle.nullDevice
        do { try process.run() } catch { return (-1, "") }
        // All commands have small bounded output (pmset and sudo -l).
        let until = ProcessInfo.processInfo.systemUptime + timeout
        while process.isRunning && ProcessInfo.processInfo.systemUptime < until { usleep(10_000) }
        if process.isRunning { kill(process.processIdentifier, SIGKILL) }
        process.waitUntilExit()
        let output = pipe.fileHandleForReading.readDataToEndOfFile()
        return (process.terminationStatus, String(decoding: output.prefix(65536), as: UTF8.self))
    }
    static func bootID() -> String {
        var size = 0
        guard sysctlbyname("kern.bootsessionuuid", nil, &size, nil, 0) == 0, size > 0, size < 256 else { return "" }
        var bytes = [CChar](repeating: 0, count: size)
        guard sysctlbyname("kern.bootsessionuuid", &bytes, &size, nil, 0) == 0 else { return "" }
        return String(cString: bytes)
    }
    static func authorizationInstalled() -> Bool {
        // An unrelated cached sudo password must not masquerade as the user's
        // explicit AO authorization, or keep it alive after this rule is removed.
        var state = stat()
        let path = rulePath(getuid())
        guard lstat(path, &state) == 0, state.st_uid == 0,
            (state.st_mode & S_IFMT) == S_IFREG, (state.st_mode & 0o777) == 0o440 else { return false }
        return true
    }
    static func authorized() -> Bool {
        guard authorizationInstalled() else { return false }
        return ["-b", "-c"].allSatisfy { flag in
            ["0", "1"].allSatisfy { value in
                run("/usr/bin/sudo", ["-n", "-l", "/usr/bin/pmset", flag, "lowpowermode", value]).0 == 0
            }
        }
    }
}

protocol WakeLowPowerDriver {
    func modes() -> [String: Bool]
    func set(_ profile: String, _ enabled: Bool) -> Bool
}
struct MacLowPowerDriver: WakeLowPowerDriver {
    func modes() -> [String: Bool] {
        let result = WakePowerCommands.run("/usr/bin/pmset", ["-g", "custom"])
        return result.0 == 0 ? WakePowerCommands.parse(result.1) : [:]
    }
    func set(_ profile: String, _ enabled: Bool) -> Bool {
        guard let flag = WakePowerCommands.flag(profile), WakePowerCommands.authorized() else { return false }
        return WakePowerCommands.run("/usr/bin/sudo", ["-n", "/usr/bin/pmset", flag, "lowpowermode", enabled ? "1" : "0"], timeout: 12).0 == 0
    }
}
struct WakeLowPowerClaim: Codable {
    var boot: String
    var phase: String // prepared must be explicitly confirmed after a crash
    var owners: [String]
}
struct WakeLowPowerJournal: Codable {
    var version = 1
    var claims: [String: WakeLowPowerClaim] = [:]
    var suppressed: [String: [String]] = [:]
}
struct WakeLowPower {
    let url: URL
    let driver: WakeLowPowerDriver
    let boot: String
    var journal: WakeLowPowerJournal
    var reason = ""
    private var batteryHeld: Set<String> = []
    private var heatHeld: Set<String> = []
    init(url: URL, driver: WakeLowPowerDriver = MacLowPowerDriver(), boot: String = WakePowerCommands.bootID()) throws {
        self.url = url; self.driver = driver; self.boot = boot
        journal = FileManager.default.fileExists(atPath: url.path) ? try read(WakeLowPowerJournal.self, url) : WakeLowPowerJournal()
        guard journal.version == 1, journal.claims.allSatisfy({ profile, claim in
            WakePowerCommands.profiles.contains(profile) && ["prepared", "applied"].contains(claim.phase) && !claim.boot.isEmpty && claim.owners.count <= 256 && claim.owners.allSatisfy { UUID(uuidString: $0) != nil }
        }), journal.suppressed.allSatisfy({ profile, owners in
            WakePowerCommands.profiles.contains(profile) && owners.count <= 256 && owners.allSatisfy { UUID(uuidString: $0) != nil }
        }) else { throw NSError(domain: "invalid Low Power Mode recovery journal", code: 1) }

    }
    mutating func save() throws { try write(journal, url) }
    mutating func reconcile(_ leases: [WakeLease], onBattery: Bool?, percent: Int?, thermal: String, force: Bool = false) -> Bool {
        reason = ""
        let live = Set(leases.map(\.id))
        batteryHeld.formIntersection(live); heatHeld.formIntersection(live)
        var wanted: [String: Set<String>] = [:]
        for lease in leases {
            let threshold = lease.lowPowerBatteryPercent ?? 0
            if threshold >= 10 && threshold <= 90, onBattery == true, let percent {
                if percent <= threshold || ((journal.claims["battery"]?.owners.contains(lease.id) ?? false) && percent < threshold + 3) { batteryHeld.insert(lease.id) }
                if percent >= threshold + 3 { batteryHeld.remove(lease.id) }
            } else { batteryHeld.remove(lease.id) }
            if lease.lowPowerOnSeriousHeat == true {
                if thermal == "serious" || thermal == "critical" || (thermal == "fair" && (journal.claims[onBattery == true ? "battery" : "charger"]?.owners.contains(lease.id) ?? false)) { heatHeld.insert(lease.id) }
                if thermal == "nominal" || thermal == "unknown" { heatHeld.remove(lease.id) }
            } else { heatHeld.remove(lease.id) }
            if batteryHeld.contains(lease.id) { wanted["battery", default: []].insert(lease.id) }
            if heatHeld.contains(lease.id), let onBattery { wanted[onBattery ? "battery" : "charger", default: []].insert(lease.id) }
        }
        do {
            for profile in WakePowerCommands.profiles {
                journal.suppressed[profile] = (journal.suppressed[profile] ?? []).filter { live.contains($0) }
                let owners = (wanted[profile] ?? []).subtracting(journal.suppressed[profile] ?? [])
                if journal.claims[profile] == nil && owners.isEmpty { continue }
                let current = driver.modes()[profile]
                if let claim = journal.claims[profile] {
                    guard let current else { reason = "low_power_status_unknown"; continue }
                    if (claim.phase == "prepared" || claim.boot != boot || boot.isEmpty) && !force { reason = "low_power_recovery_required"; continue }
                    if !current {
                        // Detectable manual changes win for the rest of these leases.
                        journal.suppressed[profile] = Array(Set((journal.suppressed[profile] ?? []) + claim.owners + Array(owners)))
                        journal.claims.removeValue(forKey: profile); try save(); continue
                    }
                    if owners.isEmpty {
                        guard driver.set(profile, false), driver.modes()[profile] == false else { reason = "low_power_restore_failed"; continue }
                        journal.claims.removeValue(forKey: profile); try save()
                    } else {
                        if Set(claim.owners) != owners { journal.claims[profile]?.owners = Array(owners); try save() }
                    }
                } else if !owners.isEmpty {
                    guard let current, !boot.isEmpty else { reason = "low_power_status_unknown"; continue }
                    if current {
                        // Treat this lease as observing a user-owned preference.
                        journal.suppressed[profile] = Array(Set((journal.suppressed[profile] ?? []) + Array(owners)))
                        try save(); continue
                    }
                    journal.claims[profile] = WakeLowPowerClaim(boot: boot, phase: "prepared", owners: Array(owners)); try save()
                    guard driver.set(profile, true), driver.modes()[profile] == true else { reason = "low_power_apply_failed"; continue }
                    journal.claims[profile]?.phase = "applied"; try save()
                }
            }
        } catch { reason = "low_power_recovery_failed" }
        return reason.isEmpty
    }
}

// Revocation never abandons an AO-owned change or another live owner's policy.
func mayRevokeLowPower(_ leases: [WakeLease], _ journal: WakeLowPowerJournal) -> Bool {
    journal.claims.isEmpty && !leases.contains { ($0.lowPowerBatteryPercent ?? 0) > 0 || $0.lowPowerOnSeriousHeat == true }
}
