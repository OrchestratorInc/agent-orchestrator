import ExpoModulesCore
import UIKit
import WatchConnectivity

public final class AOWatchModule: Module {
    public func definition() -> ModuleDefinition {
        Name("AOWatchBridge")
        Events("onWatchRequest")
        OnCreate {
            DispatchQueue.main.async {
                PhoneWatchBridge.shared.onRequest = { [weak self] event in self?.sendEvent("onWatchRequest", event) }
                PhoneWatchBridge.shared.activate()
            }
        }
        OnDestroy { DispatchQueue.main.async { PhoneWatchBridge.shared.ready = false; PhoneWatchBridge.shared.onRequest = nil } }
        AsyncFunction("publishSnapshot") { (json: String) in
            guard json.utf8.count < 48_000 else { return }
            PhoneWatchBridge.shared.publish(json)
        }.runOnQueue(.main)
        AsyncFunction("setReady") { (ready: Bool) in PhoneWatchBridge.shared.ready = ready }.runOnQueue(.main)
        AsyncFunction("isRequestActive") { (id: String) -> Bool in PhoneWatchBridge.shared.isActive(id) }.runOnQueue(.main)
        AsyncFunction("completeRequest") { (id: String, json: String) in PhoneWatchBridge.shared.complete(id, json: json) }.runOnQueue(.main)
    }
}

final class PhoneWatchBridge: NSObject, WCSessionDelegate {
    static let shared = PhoneWatchBridge()
    var ready = false
    var onRequest: (([String: Any]) -> Void)?
    private var latest: String?
    private var pending: [String: (deadline: Date, reply: ([String: Any]) -> Void)] = [:]
    private let unavailable = "{\"status\":\"open_phone\",\"message\":\"Open AO on iPhone to use wrist actions. Nothing was queued.\"}"
    private let uncertain = "{\"status\":\"uncertain\",\"message\":\"Delivery unknown. Check AO on iPhone before trying again.\"}"

    func activate() {
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }
    func publish(_ json: String) { latest = json; flush() }
    private func flush() {
        let session = WCSession.default
        guard session.activationState == .activated, session.isPaired,
              session.isWatchAppInstalled, let latest else { return }
        // Application context replaces old context; it is not an action queue.
        try? session.updateApplicationContext(["snapshot": latest])
    }
    func isActive(_ id: String) -> Bool {
        ready && UIApplication.shared.applicationState == .active && (pending[id]?.deadline ?? .distantPast) > Date()
    }
    func complete(_ id: String, json: String) {
        guard let request = pending.removeValue(forKey: id) else { return }
        request.reply(["result": json.utf8.count < 12_000 && request.deadline > Date() ? json : uncertain])
    }
    func session(_ session: WCSession, didReceiveMessage message: [String: Any], replyHandler: @escaping ([String: Any]) -> Void) {
        DispatchQueue.main.async {
            guard self.ready, UIApplication.shared.applicationState == .active, self.pending.isEmpty,
                  let receive = self.onRequest, let payload = message["payload"] as? String, payload.utf8.count < 4_000 else {
                replyHandler(["result": self.unavailable]); return
            }
            let id = UUID().uuidString
            let deadline = Date().addingTimeInterval(20)
            self.pending[id] = (deadline, replyHandler)
            receive(["id": id, "payload": payload, "deadline": deadline.timeIntervalSince1970 * 1000])
            DispatchQueue.main.asyncAfter(deadline: .now() + 20) { self.complete(id, json: self.uncertain) }
        }
    }
    func session(_ session: WCSession, activationDidCompleteWith activationState: WCSessionActivationState, error: Error?) { DispatchQueue.main.async { self.flush() } }
    func sessionWatchStateDidChange(_ session: WCSession) { DispatchQueue.main.async { self.flush() } }
    // `ready` is the JS listener + foreground handshake from WatchManager, not WCSession state.
    // A paired-Watch switch (inactive -> deactivate -> activate) must leave it intact, since
    // no AppState change follows to re-assert it; the foreground check is in `isActive`.
    func sessionDidBecomeInactive(_ session: WCSession) {}
    func sessionDidDeactivate(_ session: WCSession) { session.activate() }
}
