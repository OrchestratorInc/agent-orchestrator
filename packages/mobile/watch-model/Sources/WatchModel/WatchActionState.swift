import Foundation

struct WatchDecision: Codable, Identifiable {
    let id: String
    let label: String
    let kind: String
}
struct WatchActionContext: Codable {
    let kind: String
    let scope: String
    let requestId: String?
    let decisions: [WatchDecision]?
    let field: String?
    let minLength: Int?
    let maxLength: Int?
    let token: String
    let expiresAt: Double
}
struct WatchActionResult: Codable {
    let status: String
    var message: String? = nil
    var code: String? = nil
    var httpStatus: Int? = nil
    var context: WatchActionContext? = nil
}

struct WatchActionState {
    var context: WatchActionContext?
    var result: WatchActionResult?
    var replyText = ""
    private(set) var clientMessageId = UUID().uuidString
    var reviewedReply: String?
    var reviewedDecision: String?
    var sending = false

    mutating func editReply(_ text: String) {
        guard !sending, result == nil, text != replyText else { return }
        replyText = text
        clientMessageId = UUID().uuidString
        reviewedReply = nil
    }
    var validReply: Bool {
        guard let context, context.kind == "message" || context.kind == "input" else { return false }
        let count = replyText.unicodeScalars.count
        return !replyText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && count >= (context.minLength ?? 1) && count <= (context.maxLength ?? 500)
    }
    func canConfirm(at date: Date) -> Bool {
        guard !sending, result == nil, let context, context.expiresAt > date.timeIntervalSince1970 * 1000 else { return false }
        if context.kind == "approval" {
            return context.decisions?.contains { $0.id == reviewedDecision && ($0.kind == "allow_once" || $0.kind == "reject_once") } == true
        }
        return validReply && reviewedReply == replyText
    }
    mutating func receive(_ response: WatchActionResult) {
        sending = false
        reviewedReply = nil
        reviewedDecision = nil
        if response.status == "context", let incoming = response.context {
            context = incoming
            result = nil
        } else { result = response }
    }
}
