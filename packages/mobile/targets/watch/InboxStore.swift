import Combine
import Foundation
import WatchConnectivity
import WidgetKit

final class InboxStore: NSObject, ObservableObject, WCSessionDelegate {
    @Published var snapshot: WatchSnapshot? = WatchCache.load()
    @Published var phoneReachable = false

    init(preview: WatchSnapshot? = nil) {
        super.init()
        if let preview { snapshot = preview; return }
        #if DEBUG
        let arguments = ProcessInfo.processInfo.arguments
        if let index = arguments.firstIndex(of: "--watch-fixture"), arguments.indices.contains(index + 1) {
            snapshot = WatchFixtures.snapshot(arguments[index + 1])
            return
        }
        #endif
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    func request(_ payload: [String: String], completion: @escaping (WatchActionResult) -> Void) {
        let session = WCSession.default
        guard session.activationState == .activated, session.isReachable,
              let data = try? JSONSerialization.data(withJSONObject: payload), data.count < 4_000,
              let json = String(data: data, encoding: .utf8) else {
            completion(WatchActionResult(status: "open_phone", message: "Open AO on iPhone. Nothing was queued.")); return
        }
        var finished = false
        let finish: (WatchActionResult) -> Void = { result in
            DispatchQueue.main.async {
                guard !finished else { return }
                finished = true
                completion(result)
            }
        }
        let uncertain = WatchActionResult(status: "uncertain", message: "Delivery unknown. Check AO on iPhone before trying again.")
        session.sendMessage(["payload": json], replyHandler: { reply in
            guard let json = reply["result"] as? String, json.utf8.count < 12_000,
                  let data = json.data(using: .utf8), let result = try? JSONDecoder().decode(WatchActionResult.self, from: data) else { finish(uncertain); return }
            finish(result)
        }, errorHandler: { _ in finish(uncertain) })
        // No transferUserInfo, application-context actions, or automatic retries.
        DispatchQueue.main.asyncAfter(deadline: .now() + 25) { finish(uncertain) }
    }

    private func receive(_ context: [String: Any]) {
        guard let json = context["snapshot"] as? String, json.utf8.count < 48_000,
              let data = json.data(using: .utf8),
              let incoming = try? JSONDecoder().decode(WatchSnapshot.self, from: data), incoming.version == 1 else { return }
        DispatchQueue.main.async {
            guard incoming.generatedAt >= (self.snapshot?.generatedAt ?? 0) else { return }
            let merged = incoming.retainingCache(from: self.snapshot)
            self.snapshot = merged
            WatchCache.save(merged)
            WidgetCenter.shared.reloadTimelines(ofKind: "AOAttention")
        }
    }
    func session(_ session: WCSession, didReceiveApplicationContext applicationContext: [String: Any]) { receive(applicationContext) }
    func session(_ session: WCSession, activationDidCompleteWith activationState: WCSessionActivationState, error: Error?) {
        receive(session.receivedApplicationContext)
        sessionReachabilityDidChange(session)
    }
    func sessionReachabilityDidChange(_ session: WCSession) {
        DispatchQueue.main.async { self.phoneReachable = session.isReachable }
    }
}
