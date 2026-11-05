// Fake device and file ports only. Never starts serve or changes macOS state.
import Foundation
func processStart(_ pid: Int32) -> UInt64? { nil }
func read<T: Decodable>(_ type: T.Type, _ url: URL) throws -> T { try JSONDecoder().decode(type, from: Data(contentsOf: url)) }
func write<T: Encodable>(_ value: T, _ url: URL) throws { try JSONEncoder().encode(value).write(to: url, options: .atomic) }
final class FakeLight: WakeLightDriver {
    var boot = "boot-one"
    var current: Float? = 0.7
    var sampled: Float? = 0.7
    var writes: [Float] = []
    var journal: URL?
    var refuse = false
    func targets(_ kind: String) -> [String] { kind == "display" ? ["screen"] : [] }
    func value(_ kind: String, _ identity: String, trusted: Bool) -> Float? { current }
    func set(_ kind: String, _ identity: String, _ value: Float) -> Bool {
        // Every mutation must already be recoverable from disk.
        if value == 0, let journal {
            let saved = try! read(WakeEffectsJournal.self, journal)
            assert(saved.lights?.first?.original == 0.7)
        }
        writes.append(value)
        if refuse { return false }
        current = value
        return true
    }
    func original(_ kind: String, _ identity: String) -> Float? { sampled }
    func sampleOpen() {}
}
@main struct EffectsTests {
    static func main() throws {
        func check(_ condition: Bool) { assert(condition) }
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let url = root.appendingPathComponent("effects.json")
        let device = FakeLight(); device.journal = url
        var effects = try WakeEffects(url: url, lights: device)
        var lease = WakeLease(id: UUID().uuidString, pid: 42, started: 99, sequence: 1, writtenAt: 100)
        lease.restoreDisplay = true
        check(try effects.reconcileLights([lease], force: false))
        assert(device.writes == [0] && effects.journal.lights?.count == 1)
        // A crash after the dim write is recoverable using the same identity.
        var restarted = try WakeEffects(url: url, lights: device)
        check(try restarted.reconcileLights([], force: false))
        assert(device.writes == [0, 0.7] && restarted.journal.lights?.isEmpty == true)
        // User changes win; don't restore a saved value over nonzero brightness.
        check(try restarted.reconcileLights([lease], force: false))
        device.current = 0.4
        let before = device.writes.count
        check(try restarted.reconcileLights([], force: false))
        assert(device.current == 0.4 && device.writes.count == before)
        // Unknown/missing original brightness never produces a new mutation.
        device.sampled = nil
        check(!(try restarted.reconcileLights([lease], force: false)))
        assert(device.writes.count == before && restarted.reason == "light_sample_required")
        device.sampled = 0.7
        check(try restarted.reconcileLights([lease], force: false))
        device.boot = "different-boot"
        check(!(try restarted.reconcileLights([], force: false)))
        assert(restarted.reason == "light_recovery_required" && restarted.journal.lights?.count == 1)
        device.boot = "boot-one"; device.current = nil
        check(!(try restarted.reconcileLights([], force: false)))
        assert(restarted.journal.lights?.count == 1)
        // A refused restore keeps the ownership record for another attempt.
        device.current = 0; device.refuse = true
        check(!(try restarted.reconcileLights([], force: false)))
        assert(restarted.journal.lights?.count == 1)
        device.refuse = false
        check(try restarted.reconcileLights([], force: false))
        print("Wake light ownership and recovery tests passed")
    }
}
