import SwiftUI
import WatchKit

struct WatchActionsView: View {
    @EnvironmentObject var store: InboxStore
    let host: WatchHost
    let item: WatchItem
    @State private var state = WatchActionState()
    @State private var started = false
    @State private var showingReview = false
    private let canned = ["Please summarize.", "What is the next step?", "Please explain the blocker."]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                identity
                if let result = state.result {
                    Text(result.status == "already_handled" ? "Already handled" : result.status == "sent" ? "Sent" : result.status == "uncertain" ? "Delivery unknown" : "Open on iPhone").font(.headline)
                    Text(result.message ?? "Review this worker in AO on iPhone.")
                    if let code = result.code { Text(code).font(.caption2) }
                } else if state.sending {
                    ProgressView("Contacting iPhone…")
                } else if let context = state.context {
                    Text(context.scope).font(.body)
                    Text("Confirm within one minute. Full context is checked again before sending.").font(.caption).foregroundStyle(.secondary)
                    if context.kind == "approval" {
                        ForEach(context.decisions ?? []) { decision in
                            Button(decision.kind == "allow_once" ? "Approve once" : "Deny once") {
                                state.reviewedDecision = decision.id
                                showingReview = true
                            }
                            Text(decision.label).font(.caption)
                        }
                    } else {
                        TextField("Dictate or type a reply", text: Binding(get: { state.replyText }, set: { state.editReply($0) }))
                        ForEach(canned, id: \.self) { reply in Button(reply) { state.editReply(reply) } }
                        Text("\(state.replyText.unicodeScalars.count)/\(context.maxLength ?? 500)").font(.caption)
                        Button("Review reply") { state.reviewedReply = state.replyText; showingReview = true }.disabled(!state.validReply)
                    }
                } else { ProgressView("Checking current request…") }
            }.frame(maxWidth: .infinity, alignment: .leading)
        }
        .navigationTitle("Wrist actions")
        .onAppear {
            guard !started else { return }
            started = true
            store.request(["kind": "inspect", "hostId": host.hostId, "sessionId": item.sessionId]) { state.receive($0) }
        }
        .sheet(isPresented: $showingReview) {
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    Text("Review before sending").font(.headline)
                    identity
                    if let context = state.context {
                        Text(context.scope)
                        if let decision = context.decisions?.first(where: { $0.id == state.reviewedDecision }) {
                            Text(decision.kind == "allow_once" ? "Approve this request once" : "Deny this request once").bold()
                            Text(decision.label)
                        } else { Text(state.replyText) }
                        TimelineView(.periodic(from: .now, by: 1)) { clock in
                            Button("Confirm and send") { confirm() }.disabled(!state.canConfirm(at: clock.date))
                            if context.expiresAt <= clock.date.timeIntervalSince1970 * 1000 { Text("Expired. Go back and reopen wrist actions to refresh.").font(.caption) }
                        }
                    }
                    Button("Cancel", role: .cancel) { showingReview = false }
                }
            }
        }
    }
    private var identity: some View {
        VStack(alignment: .leading) {
            Text(host.name).font(.caption).foregroundStyle(.secondary)
            Text(item.title).font(.headline)
            Text(item.sessionId).font(.caption2)
        }
    }
    private func confirm() {
        guard state.canConfirm(at: .now), let context = state.context else { return }
        var payload = ["kind": context.kind == "approval" ? "decide" : "reply", "hostId": host.hostId, "sessionId": item.sessionId, "token": context.token]
        if context.kind == "approval" { payload["decisionId"] = state.reviewedDecision }
        else { payload["text"] = state.replyText; payload["clientMessageId"] = state.clientMessageId }
        state.sending = true
        showingReview = false
        store.request(payload) { response in
            state.receive(response)
            if response.status == "sent" { WKInterfaceDevice.current().play(.success) }
        }
    }
}
