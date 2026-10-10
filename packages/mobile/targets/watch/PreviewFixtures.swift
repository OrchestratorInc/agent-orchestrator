#if DEBUG
import SwiftUI

// Synthetic-only: usable in Xcode previews and simulator screenshot runs.
enum WatchFixtures {
    static func snapshot(_ state: String, now: Date = .now) -> WatchSnapshot {
        let age: Double = state == "stale" ? 3600 : 0
        let item = WatchItem(sessionId: "demo-worker", title: "Update welcome screen", reason: state == "blocked" ? "Agent stuck" : "Needs your input", mode: "chat")
        let items = state == "empty" ? [] : [item]
        return WatchSnapshot(version: 1, generatedAt: now.timeIntervalSince1970 * 1000, omittedHosts: 0, hosts: [WatchHost(hostId: "demo-host", name: "Demo Mac", capturedAt: (now.timeIntervalSince1970 - age) * 1000, available: state != "stale", count: items.count, items: items)])
    }
}

#Preview("Empty") { InboxView().environmentObject(InboxStore(preview: WatchFixtures.snapshot("empty"))) }
#Preview("Busy") { InboxView().environmentObject(InboxStore(preview: WatchFixtures.snapshot("busy"))) }
#Preview("Stale") { InboxView().environmentObject(InboxStore(preview: WatchFixtures.snapshot("stale"))) }
#Preview("Blocked") { InboxView().environmentObject(InboxStore(preview: WatchFixtures.snapshot("blocked"))) }
#endif
