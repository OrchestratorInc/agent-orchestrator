import ExpoModulesCore
import WatchConnectivity

public final class AOWatchModule: Module {
    public func definition() -> ModuleDefinition {
        Name("AOWatchBridge")
        OnCreate { DispatchQueue.main.async { PhoneWatchBridge.shared.activate() } }
        AsyncFunction("publishSnapshot") { (json: String) in
            guard json.utf8.count < 48_000 else { return }
            DispatchQueue.main.async { PhoneWatchBridge.shared.publish(json) }
        }
    }
}

final class PhoneWatchBridge: NSObject, WCSessionDelegate {
    static let shared = PhoneWatchBridge()
    private var latest: String?

    func activate() {
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    func publish(_ json: String) {
        latest = json
        flush()
    }

    private func flush() {
        let session = WCSession.default
        guard session.activationState == .activated, session.isPaired,
              session.isWatchAppInstalled, let latest else { return }
        // Application context replaces old context; it is not an action queue.
        try? session.updateApplicationContext(["snapshot": latest])
    }

    func session(_ session: WCSession, activationDidCompleteWith activationState: WCSessionActivationState, error: Error?) {
        DispatchQueue.main.async { self.flush() }
    }
    func sessionWatchStateDidChange(_ session: WCSession) { DispatchQueue.main.async { self.flush() } }
    func sessionDidBecomeInactive(_ session: WCSession) {}
    func sessionDidDeactivate(_ session: WCSession) { session.activate() }
}
