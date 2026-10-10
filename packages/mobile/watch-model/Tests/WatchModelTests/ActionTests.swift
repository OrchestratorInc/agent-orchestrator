import Foundation

struct ActionTests {
    static func run() {
        let context = WatchActionContext(kind: "message", scope: "Reply", requestId: nil, decisions: nil, field: nil, minLength: 1, maxLength: 50, token: "token", expiresAt: 10_000)
        var state = WatchActionState()
        state.receive(WatchActionResult(status: "context", context: context))
        state.editReply("Please summarize.")
        assert(!state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        state.reviewedReply = state.replyText
        assert(state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        let firstID = state.clientMessageId
        state.editReply("Please explain the blocker.")
        assert(firstID != state.clientMessageId)
        assert(!state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        state.reviewedReply = state.replyText
        assert(!state.canConfirm(at: Date(timeIntervalSince1970: 10)))
        state.sending = true
        assert(!state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        state.receive(WatchActionResult(status: "uncertain"))
        assert(!state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        assert(state.clientMessageId != firstID)

        let approval = WatchActionContext(kind: "approval", scope: "git status", requestId: "r", decisions: [WatchDecision(id: "allow", label: "Allow", kind: "allow_once")], field: nil, minLength: nil, maxLength: nil, token: "token", expiresAt: 10_000)
        state.receive(WatchActionResult(status: "context", context: approval))
        assert(!state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        state.reviewedDecision = "not-offered"
        assert(!state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        state.reviewedDecision = "allow"
        assert(state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        state.receive(WatchActionResult(status: "already_handled"))
        assert(!state.canConfirm(at: Date(timeIntervalSince1970: 1)))
        print("Watch action review checks passed")
    }
}
