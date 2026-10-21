// Fake power driver only: never authorizes or changes macOS power settings.
import Foundation
func read<T: Decodable>(_ type: T.Type, _ url: URL) throws -> T { try JSONDecoder().decode(type, from: Data(contentsOf: url)) }
func write<T: Encodable>(_ value: T, _ url: URL) throws { try JSONEncoder().encode(value).write(to: url, options: .atomic) }
final class FakePower: WakeLowPowerDriver {
    var values = ["battery": false, "charger": false]
    var writes: [String] = []
    var reads = 0
    var refuse = false
    var url: URL!
    func modes() -> [String: Bool] { reads += 1; return values }
    func set(_ profile: String, _ enabled: Bool) -> Bool {
        let journal = try! read(WakeLowPowerJournal.self, url)
        assert(journal.claims[profile] != nil) // journal before every mutation
        writes.append(profile + ":" + String(enabled))
        if refuse { return false }
        values[profile] = enabled
        return true
    }
}
@main struct LowPowerTests {
    static func main() throws {
        assert(WakePowerCommands.parse("Battery Power:\n lowpowermode 1\nAC Power:\n powermode 0") == ["battery": true, "charger": false])
        assert(WakePowerCommands.parse("Battery Power:\n powermode 2\nAC Power:\n lowpowermode ?").isEmpty)
        assert(WakePowerCommands.parse("Battery Power:\n lowpowermode\n powermode 1").isEmpty)
        assert(WakePowerCommands.flag("-a") == nil)
        let rule = WakePowerCommands.rule(501)
        assert(rule.contains("#501 ALL=(root) TIMEOUT=5s NOPASSWD:"))
        assert(rule.components(separatedBy: "/usr/bin/pmset").count == 5)
        assert(!rule.contains("ALL=(root) NOPASSWD: ALL") && !rule.contains("*"))
        assert(WakePowerCommands.authorizationScript(501).contains("with administrator privileges"))
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let ruleURL = root.appendingPathComponent("sudoers-rule")
        try Data(rule.utf8).write(to: ruleURL)
        let syntax = WakePowerCommands.run("/usr/sbin/visudo", ["-c", "-f", ruleURL.path])
        assert(syntax.0 == 0, syntax.1) // syntax check only; no installation
        let script = WakePowerCommands.run("/usr/bin/osacompile", ["-o", root.appendingPathComponent("authorize.scpt").path, "-e", WakePowerCommands.authorizationScript(501)])
        assert(script.0 == 0, script.1) // compile only; never execute the prompt/script
        let revoke = WakePowerCommands.revocationScript(501)
        assert(revoke == "do shell script \"/bin/rm -f /etc/sudoers.d/ao-low-power-501\" with administrator privileges")
        assert(!revoke.contains("*") && !revoke.contains("pmset"))
        assert(WakePowerCommands.rulePath(UInt32.max) == "/etc/sudoers.d/ao-low-power-4294967295")
        let revokeSyntax = WakePowerCommands.run("/usr/bin/osacompile", ["-o", root.appendingPathComponent("revoke.scpt").path, "-e", revoke])
        assert(revokeSyntax.0 == 0, revokeSyntax.1) // compile only; never remove a grant
        try WakePowerCommands.withAuthorizationLock(root) {
            do {
                try WakePowerCommands.withAuthorizationLock(root) { assertionFailure("authorization lock was not exclusive") }
            } catch { /* a coordinator cannot mutate power during revocation */ }
        }
        try WakePowerCommands.withAuthorizationLock(root) { } // release on scope exit
        assert(!WakePowerCommands.revocationPending(root))
        try write(true, WakePowerCommands.revocationMarker(root))
        assert(WakePowerCommands.revocationPending(root))
        // The barrier survives helper reconstruction until explicit retry.
        assert(WakePowerCommands.revocationPending(root))
        try FileManager.default.removeItem(at: WakePowerCommands.revocationMarker(root))
        assert(!WakePowerCommands.revocationPending(root))
        var revokeLease = WakeLease(id: UUID().uuidString, pid: 42, started: 99, sequence: 1, writtenAt: 100)
        assert(mayRevokeLowPower([revokeLease], WakeLowPowerJournal()))
        revokeLease.lowPowerBatteryPercent = 30
        assert(!mayRevokeLowPower([revokeLease], WakeLowPowerJournal()))
        revokeLease.lowPowerBatteryPercent = 0
        revokeLease.lowPowerOnSeriousHeat = true
        assert(!mayRevokeLowPower([revokeLease], WakeLowPowerJournal()))
        let unresolved = WakeLowPowerJournal(claims: ["battery": WakeLowPowerClaim(boot: "one", phase: "applied", owners: [])])
        assert(!mayRevokeLowPower([], unresolved))
        let url = root.appendingPathComponent("power.json")
        let driver = FakePower(); driver.url = url
        var power = try WakeLowPower(url: url, driver: driver, boot: "one")
        var lease = WakeLease(id: UUID().uuidString, pid: 42, started: 99, sequence: 1, writtenAt: 100)
        assert(power.reconcile([lease], onBattery: true, percent: 80, thermal: "nominal"))
        assert(driver.reads == 0 && driver.writes.isEmpty) // disabled policy does no host work
        lease.lowPowerBatteryPercent = 30
        func check(_ ok: Bool) { assert(ok) }
        check(power.reconcile([lease], onBattery: true, percent: 30, thermal: "nominal"))
        assert(driver.writes == ["battery:true"])
        check(power.reconcile([lease], onBattery: true, percent: 32, thermal: "nominal"))
        assert(driver.writes.count == 1) // hysteresis
        check(power.reconcile([lease], onBattery: true, percent: 33, thermal: "nominal"))
        assert(driver.writes.last == "battery:false")
        // A pre-existing preference is never adopted or restored.
        driver.values["battery"] = true
        check(power.reconcile([lease], onBattery: true, percent: 20, thermal: "nominal"))
        check(power.reconcile([], onBattery: true, percent: 20, thermal: "nominal"))
        assert(driver.values["battery"] == true && power.journal.claims.isEmpty)
        driver.values["battery"] = false
        // Heat changes only the current profile; recovery waits until nominal.
        lease.lowPowerOnSeriousHeat = true
        check(power.reconcile([lease], onBattery: false, percent: 80, thermal: "serious"))
        assert(driver.values["charger"] == true && driver.values["battery"] == false)
        check(power.reconcile([lease], onBattery: false, percent: 80, thermal: "fair"))
        assert(driver.values["charger"] == true)
        // Multi-owner requests keep the preference until all owners leave.
        var other = lease; other = WakeLease(id: UUID().uuidString, pid: 43, started: 100, sequence: 1, writtenAt: 100, lowPowerOnSeriousHeat: true)
        check(power.reconcile([lease, other], onBattery: false, percent: 80, thermal: "serious"))
        check(power.reconcile([other], onBattery: false, percent: 80, thermal: "serious"))
        assert(driver.values["charger"] == true)
        // Crash recovery restores an acknowledged AO change.
        var restarted = try WakeLowPower(url: url, driver: driver, boot: "one")
        check(restarted.reconcile([], onBattery: nil, percent: nil, thermal: "unknown"))
        assert(driver.values["charger"] == false)
        check(restarted.reconcile([lease], onBattery: true, percent: 20, thermal: "nominal"))
        driver.values["battery"] = false // user's override wins while lease remains
        check(restarted.reconcile([lease], onBattery: true, percent: 20, thermal: "nominal"))
        check(restarted.reconcile([lease], onBattery: true, percent: 20, thermal: "nominal"))
        assert(driver.values["battery"] == false)
        check(restarted.reconcile([], onBattery: true, percent: 20, thermal: "nominal"))
        driver.refuse = true
        assert(!restarted.reconcile([lease], onBattery: true, percent: 20, thermal: "nominal"))
        assert(restarted.journal.claims["battery"]?.phase == "prepared")
        driver.refuse = false; driver.values["battery"] = true // interrupted command completed
        var uncertain = try WakeLowPower(url: url, driver: driver, boot: "one")
        assert(!uncertain.reconcile([], onBattery: nil, percent: nil, thermal: "unknown"))
        assert(uncertain.reason == "low_power_recovery_required")
        check(uncertain.reconcile([], onBattery: nil, percent: nil, thermal: "unknown", force: true))
        check(uncertain.reconcile([lease], onBattery: true, percent: 20, thermal: "nominal"))
        var rebooted = try WakeLowPower(url: url, driver: driver, boot: "two")
        assert(!rebooted.reconcile([], onBattery: nil, percent: nil, thermal: "unknown"))
        driver.refuse = true
        assert(!rebooted.reconcile([], onBattery: nil, percent: nil, thermal: "unknown", force: true))
        assert(rebooted.journal.claims["battery"] != nil)
        driver.refuse = false
        check(rebooted.reconcile([], onBattery: nil, percent: nil, thermal: "unknown", force: true))
        assert(rebooted.journal.claims.isEmpty)
        // An unreadable profile is never mutated.
        driver.values.removeValue(forKey: "battery")
        assert(!rebooted.reconcile([lease], onBattery: true, percent: 20, thermal: "nominal"))
        let invalid = "{\"version\":1,\"claims\":{\"battery\":{\"boot\":\"one\",\"phase\":\"unknown\",\"owners\":[]}},\"suppressed\":{}}"
        try Data(invalid.utf8).write(to: url)
        do { _ = try WakeLowPower(url: url, driver: driver, boot: "one"); assertionFailure("invalid journal was adopted") }
        catch { /* Preserve an invalid journal without mutating a device. */ }
        print("Low Power Mode policy, authorization-contract and recovery tests passed")
    }
}
