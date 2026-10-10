import SwiftUI

@main
struct AOWatchApp: App {
    @StateObject private var store = InboxStore()
    var body: some Scene { WindowGroup { InboxView().environmentObject(store) } }
}

struct InboxView: View {
    @EnvironmentObject var store: InboxStore
    var body: some View {
        NavigationStack {
            TimelineView(.periodic(from: .now, by: 30)) { context in
                List {
                    Section {
                        Text("\(store.snapshot?.countLabel(at: context.date) ?? "—") need you")
                            .font(.title2.bold())
                        FreshnessView(snapshot: store.snapshot, date: context.date)
                        if !store.phoneReachable { Label("iPhone unavailable", systemImage: "iphone.slash").font(.caption) }
                    }
                    if let snapshot = store.snapshot {
                        ForEach(snapshot.hosts) { host in
                            Section(host.name) {
                                if !host.isCurrent(at: context.date) { Text("Last known · refresh on iPhone").font(.caption) }
                                ForEach(host.items) { item in
                                    NavigationLink { WatchDetail(host: host, item: item) } label: {
                                        VStack(alignment: .leading) {
                                            Text(item.title).lineLimit(2)
                                            Text(item.reason).font(.caption).foregroundStyle(.secondary)
                                        }
                                    }
                                }
                                if let count = host.count, count > host.items.count { Text("\(count - host.items.count) more on iPhone").font(.caption) }
                            }
                        }
                        if snapshot.isCurrent(at: context.date) && snapshot.knownCount == 0 { Text("Nothing needs you") }
                        if snapshot.omittedHosts > 0 { Text("More machines on iPhone") }
                    } else { Text("Open AO on iPhone to load your inbox.") }
                }
                .navigationTitle("Needs you")
            }
        }
    }
}

struct FreshnessView: View {
    let snapshot: WatchSnapshot?
    let date: Date
    var body: some View {
        VStack(alignment: .leading) {
            Text(snapshot?.isCurrent(at: date) == true ? "Phone snapshot" : "Stale or unavailable")
            if let captured = snapshot?.oldestCapture { Text(captured, style: .relative) + Text(" ago") }
        }.font(.caption).foregroundStyle(.secondary)
    }
}

struct WatchDetail: View {
    @EnvironmentObject var store: InboxStore
    let host: WatchHost
    let item: WatchItem
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                Text(host.name).font(.caption).foregroundStyle(.secondary)
                Text(item.title).font(.headline)
                Text(item.reason)
                TimelineView(.periodic(from: .now, by: 30)) { context in
                    Text(host.isCurrent(at: context.date) ? "Phone snapshot" : "Stale · refresh on iPhone").font(.caption)
                    if let captured = host.capturedAt { Text(Date(timeIntervalSince1970: captured / 1000), style: .relative).font(.caption) }
                }
                if item.mode == "chat" {
                    NavigationLink("Wrist actions") { WatchActionsView(host: host, item: item) }
                }
                Label("Open on iPhone", systemImage: "iphone")
                Text("Open AO and select this worker. This does not launch the phone app.").font(.caption).foregroundStyle(.secondary)
            }.frame(maxWidth: .infinity, alignment: .leading)
        }.navigationTitle("Worker")
    }
}
