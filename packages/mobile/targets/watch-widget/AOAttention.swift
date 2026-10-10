import SwiftUI
import WidgetKit

struct AttentionEntry: TimelineEntry {
    let date: Date
    let snapshot: WatchSnapshot?
}
struct AttentionProvider: TimelineProvider {
    func placeholder(in context: Context) -> AttentionEntry { AttentionEntry(date: .now, snapshot: nil) }
    func getSnapshot(in context: Context, completion: @escaping (AttentionEntry) -> Void) { completion(AttentionEntry(date: .now, snapshot: WatchCache.load())) }
    func getTimeline(in context: Context, completion: @escaping (Timeline<AttentionEntry>) -> Void) {
        let snapshot = WatchCache.load()
        let now = Date()
        // Pre-schedule the stale boundary; a delayed widget reload must not promise fresh zero.
        let expiry = snapshot?.oldestCapture?.addingTimeInterval(120) ?? now
        var entries = [AttentionEntry(date: now, snapshot: snapshot)]
        if expiry > now { entries.append(AttentionEntry(date: expiry, snapshot: snapshot)) }
        completion(Timeline(entries: entries, policy: .after(now.addingTimeInterval(900))))
    }
}
struct AttentionView: View {
    @Environment(\.widgetFamily) var family
    let entry: AttentionEntry
    var body: some View {
        let count = entry.snapshot?.countLabel(at: entry.date) ?? "—"
        let current = entry.snapshot?.isCurrent(at: entry.date) == true
        Group {
            if family == .accessoryInline { Text("AO \(count) · \(current ? "needs you" : "stale")") }
            else {
                VStack(alignment: family == .accessoryRectangular ? .leading : .center) {
                    Text(count).font(.title2.bold())
                    Text(current ? "Needs you" : "Stale").font(.caption2)
                    if family == .accessoryRectangular, let captured = entry.snapshot?.oldestCapture { Text(captured, style: .relative).font(.caption2) }
                }
            }
        }
        .containerBackground(.fill.tertiary, for: .widget)
        .privacySensitive()
        .accessibilityLabel("AO \(count) need you. \(current ? "Phone snapshot" : "Stale or unavailable; open AO on iPhone")")
    }
}
@main
struct AOAttention: Widget {
    let kind = "AOAttention"
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: AttentionProvider()) { AttentionView(entry: $0) }
            .configurationDisplayName("AO Needs you")
            .description("Last-known attention across your machines. Open AO on iPhone to refresh.")
            .supportedFamilies([.accessoryCircular, .accessoryRectangular, .accessoryInline])
    }
}
