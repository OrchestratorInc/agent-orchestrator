import Foundation

@main
struct SnapshotTests {
    static func main() {
        ActionTests.run()
        let tests = SnapshotTests()
        tests.testOnlyFreshCompleteSnapshotCanShowZero()
        tests.testLastKnownAttentionStaysVisibleButNotCurrent()
        tests.testUnknownPhoneSnapshotRetainsWatchCacheButForgettingRemovesIt()
        tests.testIncompleteHostCoverageDoesNotClaimZero()
        print("Watch snapshot checks passed")
    }
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    func snapshot(count: Int? = 0, age: Double = 0, available: Bool = true) -> WatchSnapshot {
        WatchSnapshot(version: 1, generatedAt: now.timeIntervalSince1970 * 1000, omittedHosts: 0, hosts: [WatchHost(hostId: "alpha", name: "Mac", capturedAt: (now.timeIntervalSince1970 - age) * 1000, available: available, count: count, items: [])])
    }
    func testOnlyFreshCompleteSnapshotCanShowZero() {
        assertEqual(snapshot().countLabel(at: now), "0")
        assertEqual(snapshot(age: 121).countLabel(at: now), "—")
        assertEqual(snapshot(available: false).countLabel(at: now), "—")
        assertEqual(snapshot(count: nil).countLabel(at: now), "—")
        assertEqual(snapshot(age: -60).countLabel(at: now), "—")
    }
    func testLastKnownAttentionStaysVisibleButNotCurrent() {
        assertEqual(snapshot(count: 3, age: 180).countLabel(at: now), "3+")
        assertFalse(snapshot(age: 180).isCurrent(at: now))
    }
    func testUnknownPhoneSnapshotRetainsWatchCacheButForgettingRemovesIt() {
        let cached = snapshot(count: 3)
        let unknown = WatchSnapshot(version: 1, generatedAt: cached.generatedAt + 1, omittedHosts: 0, hosts: [WatchHost(hostId: "alpha", name: "Mac", capturedAt: nil, available: false, count: nil, items: [])])
        assertEqual(unknown.retainingCache(from: cached).hosts[0].count, 3)
        assertFalse(unknown.retainingCache(from: cached).hosts[0].available)
        let forgotten = WatchSnapshot(version: 1, generatedAt: cached.generatedAt + 1, omittedHosts: 0, hosts: [])
        assert(forgotten.retainingCache(from: cached).hosts.isEmpty)
        assertEqual(forgotten.countLabel(at: now), "—")
    }
    func testIncompleteHostCoverageDoesNotClaimZero() {
        var partial = snapshot()
        partial.omittedHosts = 1
        assertEqual(partial.countLabel(at: now), "—")
    }
}

private func assertEqual<T: Equatable>(_ lhs: T, _ rhs: T) { assert(lhs == rhs, "\(lhs) != \(rhs)") }
private func assertFalse(_ value: Bool) { assert(!value) }
