import Foundation

@main struct PolicyTests {
    static func main() throws {
        func lease(_ id: String, pid: Int32 = 42, start: UInt64 = 99,
                   sequence: UInt64 = 1, wall: Double = 100) -> WakeLease {
            WakeLease(id: id, pid: pid, started: start, sequence: sequence, writtenAt: wall)
        }
        assert(!WakePolicy.supportsOS(majorVersion: 12))
        for version in 13...27 { assert(WakePolicy.supportsOS(majorVersion: version)) }
        assert(!WakePolicy.supportsOS(majorVersion: 28))
        let a = "2b49c35b-e73e-4f28-908a-4f927ce9d322"
        let b = "53696c01-210c-4687-9f45-f7e3dcd6f91e"
        let identity: (Int32) -> UInt64? = { $0 == 42 ? 99 : nil }
        var policy = WakePolicy()
        assert(policy.live([lease(a), lease(b)], now: 0, wall: 100, identity: identity).count == 2)
        // Losing one daemon doesn't release the other owner's live lease.
        assert(policy.live([lease(b, sequence: 2)], now: 30, wall: 130, identity: identity).count == 1)
        assert(policy.live([lease(b, sequence: 2)], now: 89, wall: 189, identity: identity).count == 1)
        assert(policy.live([lease(b, sequence: 2)], now: 90, wall: 190, identity: identity).isEmpty)
        // An altered wall clock cannot extend an already observed lease.
        var clockPolicy = WakePolicy()
        assert(clockPolicy.live([lease(a)], now: 0, wall: 100, identity: identity).count == 1)
        assert(clockPolicy.live([lease(a)], now: 60, wall: 0, identity: identity).isEmpty)
        var restarted = WakePolicy()
        assert(restarted.live([lease(a)], now: 0, wall: 161, identity: identity).isEmpty)
        var remaining = WakePolicy()
        assert(remaining.live([lease(a)], now: 0, wall: 159, identity: identity).count == 1)
        assert(remaining.live([lease(a)], now: 1, wall: 160, identity: identity).isEmpty)
        assert(restarted.live([lease(a, start: 100)], now: 0, wall: 100, identity: identity).isEmpty)
        assert(restarted.live([lease(a, pid: 43)], now: 0, wall: 100, identity: identity).isEmpty)
        assert(WakePolicy.safety(thermalCritical: true, onBattery: false, percent: 100) == "critical_heat")
        assert(WakePolicy.safety(thermalCritical: false, onBattery: true, percent: 9) == "low_battery")
        assert(WakePolicy.safety(thermalCritical: false, onBattery: true, percent: 10) == nil)
        assert(WakePolicy.safety(thermalCritical: false, onBattery: false, percent: nil) == nil)
        assert(WakePolicy.safety(thermalCritical: false, onBattery: nil, percent: nil) == "battery_unknown")
        // Default/mobile and explicit lid requests work without optional effects.
        assert(lease(a).wantsLid && !lease(a).wantsEffects)
        var desktop = lease(b)
        desktop.lidClosed = true
        assert(desktop.wantsLid && !desktop.wantsEffects)
        desktop.lidClosed = false
        desktop.muteAudio = true
        assert(!desktop.wantsLid && !desktop.wantsEffects)
        var finite = lease(a)
        finite.expiresAt = 120
        var finitePolicy = WakePolicy()
        assert(finitePolicy.live([finite], now: 0, wall: 100, identity: identity).count == 1)
        assert(finitePolicy.live([finite], now: 20, wall: 90, identity: identity).isEmpty)
        // Go bounds an aggregate containing a session-bound lid contribution
        // to 15s, or to the expiry of a finite lid request. A stalled loop must
        // not be rescued by the independent 15s sequence-renewal loop, even
        // when another idle-only owner is indefinite or lasts an hour.
        for idleExpiry: Double? in [nil, 3700] {
            for aggregateExpiry: Double in [115, 130] {
                var overlapPolicy = WakePolicy()
                for seconds in stride(from: 0, through: 120, by: 15) {
                    let wall = 100 + Double(seconds)
                    let sequence = UInt64(seconds / 15 + 1)
                    var aggregate = lease(a, sequence: sequence, wall: wall)
                    aggregate.lidClosed = true
                    aggregate.expiresAt = aggregateExpiry
                    // Separate idle control proves sequence renewals are live;
                    // expiry, rather than dead-process detection, drops the lid.
                    var idle = lease(b, sequence: sequence, wall: wall)
                    idle.lidClosed = false
                    idle.expiresAt = idleExpiry
                    let live = overlapPolicy.live([aggregate, idle], now: Double(seconds),
                                                  wall: wall, identity: identity)
                    assert(live.count == (wall < aggregateExpiry ? 2 : 1))
                    assert(live.contains(where: { $0.wantsLid }) == (wall < aggregateExpiry))
                }
            }
        }
        assert(WakePolicy.safety(thermalCritical: false, onBattery: true, percent: 29, minimumPercent: 30) == "low_battery")
        assert(WakePolicy.safety(thermalCritical: false, onBattery: true, percent: 9, minimumPercent: 0) == "low_battery")
        assert(!WakeLightPolicy.supportsOS(15))
        assert(WakeLightPolicy.supportsOS(26) && WakeLightPolicy.supportsOS(27))
        assert(!WakeLightPolicy.supportsOS(28))
        assert(WakeLightPolicy.keyboardCompatible(WakeLightPolicy.keyboardSignatures))
        var badABI = WakeLightPolicy.keyboardSignatures
        badABI["brightnessForKeyboard:"] = "d@:Q"
        assert(!WakeLightPolicy.keyboardCompatible(badABI))
        badABI.removeValue(forKey: "brightnessForKeyboard:")
        assert(!WakeLightPolicy.keyboardCompatible(badABI))
        var events: [String] = []
        var owner = WakeOwnership(journal: WakeJournal())
        let acquired = try owner.acquire(persist: { assert($0.owned); events.append("persist") },
                                 disable: { events.append("disable"); return true })
        assert(acquired)
        assert(events == ["persist", "disable"])
        // Failure to restore keeps the journal recoverable for a restarted helper.
        assert(!owner.release(persist: { _ in fatalError("must not clear failed recovery") }, restore: { false }))
        assert(owner.journal.owned)
        var recovered = WakeOwnership(journal: try JSONDecoder().decode(WakeJournal.self, from: JSONEncoder().encode(owner.journal)))
        assert(recovered.release(persist: { assert(!$0.owned) }, restore: { true }))
        assert(!recovered.journal.owned)
        enum Failure: Error { case io }
        var failing = WakeOwnership(journal: WakeJournal())
        do {
            _ = try failing.acquire(persist: { _ in throw Failure.io }, disable: { fatalError("must not mutate after failed journal write") })
            fatalError("expected journal failure")
        } catch Failure.io { }
        // A failed ownership-clear write retries restoration next time.
        assert(!owner.release(persist: { _ in throw Failure.io }, restore: { true }))
        assert(owner.journal.owned)
        var unowned = WakeOwnership(journal: WakeJournal())
        assert(unowned.release(persist: { _ in fatalError() }, restore: { fatalError("must not clear another owner's control") }))
        // Persist a startup safety latch before restoration can suspend the Mac.
        var safetyOwner = WakeOwnership(journal: WakeJournal(owned: true))
        var safetyEvents: [String] = []
        var safetySaved = Data()
        try safetyOwner.latch([lease(a)], reason: "critical_heat", persist: {
            safetySaved = try JSONEncoder().encode($0)
            safetyEvents.append("latch")
        })
        assert(safetyOwner.release(persist: { _ in }, restore: {
            safetyEvents.append("restore"); return true
        }))
        assert(safetyEvents == ["latch", "restore"])
        var safetyRestart = WakeOwnership(journal: try JSONDecoder().decode(WakeJournal.self, from: safetySaved))
        try safetyRestart.latch([lease(a, sequence: 2)], reason: nil, persist: { _ in fatalError("cooling must not clear latch") })
        try safetyRestart.latch([lease(a, sequence: 3)], reason: "low_battery", persist: { _ in fatalError("existing latch remains") })
        assert(safetyRestart.journal.blocked[a] == "critical_heat")
        var display = WakeDisplayPolicy()
        assert(!display.needsSleep(closedUndocked: false))
        assert(display.needsSleep(closedUndocked: true))
        for _ in 0..<100 { assert(!display.needsSleep(closedUndocked: true)) }
        // Opening, docking or withdrawing ownership resets the transition.
        assert(!display.needsSleep(closedUndocked: false))
        assert(display.needsSleep(closedUndocked: true))
        print("Wake policy and recovery tests passed")
    }
}
