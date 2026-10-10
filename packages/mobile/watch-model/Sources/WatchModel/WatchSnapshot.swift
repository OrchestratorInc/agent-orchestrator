import Foundation

struct WatchItem: Codable, Identifiable, Equatable {
    var sessionId: String
    var title: String
    var reason: String
    var mode: String
    var id: String { sessionId }
}

struct WatchHost: Codable, Identifiable, Equatable {
    var hostId: String
    var name: String
    var capturedAt: Double?
    var available: Bool
    var count: Int?
    var items: [WatchItem]
    var id: String { hostId }

    func isCurrent(at date: Date) -> Bool {
        guard available, let capturedAt, count != nil else { return false }
        let age = date.timeIntervalSince1970 - capturedAt / 1000
        return age >= 0 && age < 120
    }
}

struct WatchSnapshot: Codable, Equatable {
    var version: Int
    var generatedAt: Double
    var omittedHosts: Int
    var hosts: [WatchHost]

    func isCurrent(at date: Date) -> Bool {
        version == 1 && omittedHosts == 0 && !hosts.isEmpty && hosts.allSatisfy { $0.isCurrent(at: date) }
    }
    var knownCount: Int { hosts.reduce(0) { $0 + ($1.count ?? 0) } }
    func countLabel(at date: Date) -> String {
        if isCurrent(at: date) { return "\(knownCount)" }
        return knownCount > 0 ? "\(knownCount)+" : "—"
    }
    var oldestCapture: Date? {
        hosts.compactMap(\.capturedAt).min().map { Date(timeIntervalSince1970: $0 / 1000) }
    }
    func retainingCache(from previous: WatchSnapshot?) -> WatchSnapshot {
        var result = self
        result.hosts = hosts.map { host in
            guard host.capturedAt == nil, let cached = previous?.hosts.first(where: { $0.id == host.id }) else { return host }
            var retained = cached
            retained.name = host.name
            retained.available = false
            return retained
        }
        return result
    }
}

// Shared on the Watch only. The phone's bearer/config never enters this suite.
enum WatchCache {
    static let group = "group.aoagents.ao.watch"
    static let key = "needs-you-v1"
    static func load() -> WatchSnapshot? {
        guard let data = UserDefaults(suiteName: group)?.data(forKey: key),
              let snapshot = try? JSONDecoder().decode(WatchSnapshot.self, from: data), snapshot.version == 1 else { return nil }
        return snapshot
    }
    static func save(_ snapshot: WatchSnapshot) {
        guard let data = try? JSONEncoder().encode(snapshot) else { return }
        UserDefaults(suiteName: group)?.set(data, forKey: key)
    }
}
