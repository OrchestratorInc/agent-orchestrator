import Foundation

// A lease is bound to a process start identity, not just a reusable PID.
struct WakeLease: Codable {
    let id: String
    let pid: Int32
    let started: UInt64
    let sequence: UInt64
    let writtenAt: Double
}

struct WakeJournal: Codable {
    var owned = false
    var blocked: [String: String] = [:]
}

struct WakeStatus: Codable {
    let version: Int
    let updatedAt: Double
    let active: Bool
    let reason: String
    let leases: [String]
    let blocked: [String: String]
}

struct WakePolicy {
    // This is a capability range, not evidence of physical lid testing.
    static func supportsOS(majorVersion: Int) -> Bool {
        (13...27).contains(majorVersion)
    }

    // Renewals extend monotonic deadlines only when their sequence advances.
    // Old files cannot keep a hung daemon awake after their deadline.
    private var observed: [String: (UInt64, Double)] = [:]

    mutating func live(_ leases: [WakeLease], now: Double, wall: Double,
                       identity: (Int32) -> UInt64?) -> [WakeLease] {
        let present = Set(leases.map(\.id))
        observed = observed.filter { present.contains($0.key) }
        return leases.filter { lease in
            guard identity(lease.pid) == lease.started,
                  lease.started > 0, lease.pid > 1,
                  UUID(uuidString: lease.id) != nil else { return false }
            if let entry = observed[lease.id] {
                if lease.sequence > entry.0 {
                    observed[lease.id] = (lease.sequence, now)
                }
            } else {
                // A restart must not renew an abandoned on-disk request.
                guard wall - lease.writtenAt >= -5, wall - lease.writtenAt < 60 else { return false }
                observed[lease.id] = (lease.sequence, now - max(0, wall - lease.writtenAt))
            }
            return now - (observed[lease.id]?.1 ?? now) < 60
        }
    }

    static func safety(thermalCritical: Bool, onBattery: Bool?, percent: Int?) -> String? {
        if thermalCritical { return "critical_heat" }
        guard let onBattery else { return "battery_unknown" }
        if onBattery {
            guard let percent else { return "battery_unknown" }
            if percent < 10 { return "low_battery" }
        }
        return nil
    }
}

// Persisted ownership makes a failed acquisition recoverable. Callbacks are
// explicit so crash boundaries and failed writes can be tested without IOKit.
struct WakeOwnership {
    var journal: WakeJournal

    mutating func latch(_ leases: [WakeLease], reason: String?,
                        persist: (WakeJournal) throws -> Void) throws {
        guard let reason else { return }
        var changed = false
        for lease in leases where journal.blocked[lease.id] == nil {
            journal.blocked[lease.id] = reason
            changed = true
        }
        if changed { try persist(journal) }
    }

    mutating func acquire(persist: (WakeJournal) throws -> Void,
                          disable: () -> Bool) throws -> Bool {
        journal.owned = true
        try persist(journal)
        return disable()
    }

    mutating func release(persist: (WakeJournal) throws -> Void,
                          restore: () -> Bool) -> Bool {
        guard journal.owned else { return true }
        guard restore() else { return false }
        journal.owned = false
        do { try persist(journal) } catch { journal.owned = true; return false }
        return true
    }
}

// Display sleep is a transition action, not work repeated by every lease scan.
struct WakeDisplayPolicy {
    private var previouslyClosedUndocked = false

    mutating func needsSleep(closedUndocked: Bool) -> Bool {
        let needed = closedUndocked && !previouslyClosedUndocked
        previouslyClosedUndocked = closedUndocked
        return needed
    }
}
