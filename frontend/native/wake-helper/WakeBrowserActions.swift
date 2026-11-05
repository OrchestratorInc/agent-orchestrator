import Foundation
import AppKit
import Security

struct WakeBrowserTarget {
    let id: String
    let name: String
    let team: String
}
let wakeBrowserTargets = [
    WakeBrowserTarget(id: "com.google.Chrome", name: "Google Chrome", team: "EQHXZ8M8AV"),
    WakeBrowserTarget(id: "com.brave.Browser", name: "Brave", team: "KL8N8XSYF4")
]
func trustedBrowser(_ target: WakeBrowserTarget, strict: Bool = false) -> URL? {
    guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: target.id),
          Bundle(url: url)?.bundleIdentifier == target.id else { return nil }
    var code: SecStaticCode?
    guard SecStaticCodeCreateWithPath(url as CFURL, [], &code) == errSecSuccess, let code else { return nil }
    var requirement: SecRequirement?
    let rule = "anchor apple generic and identifier \"\(target.id)\" and certificate leaf[subject.OU] = \"\(target.team)\""
    guard SecRequirementCreateWithString(rule as CFString, [], &requirement) == errSecSuccess else { return nil }
    let flags = strict ? SecCSFlags(rawValue: kSecCSStrictValidate) : SecCSFlags(rawValue: kSecCSBasicValidateOnly)
    guard SecStaticCodeCheckValidity(code, flags, requirement) == errSecSuccess else { return nil }
    return url
}
func browserManaged(_ app: NSRunningApplication) -> Bool {
    guard processStart(app.processIdentifier) != nil,
          !WakeEffects.guardedProcesses([getppid()]).contains(app.processIdentifier) else { return true }
    let command = Process()
    command.executableURL = URL(fileURLWithPath: "/bin/ps")
    command.arguments = ["-p", String(app.processIdentifier), "-o", "command="]
    let pipe = Pipe(); command.standardOutput = pipe; command.standardError = FileHandle.nullDevice
    do { try command.run() } catch { return true }
    let data = pipe.fileHandleForReading.readData(ofLength: 65536)
    if command.isRunning { command.terminate() }
    command.waitUntilExit()
    guard data.count < 65536, let text = String(data: data, encoding: .utf8), !text.isEmpty else { return true }
    return text.contains("--remote-debugging") || text.contains("/.ao/") || text.contains("--headless")
}
func relaunchWakeBrowser(_ id: String) throws {
    guard let target = wakeBrowserTargets.first(where: { $0.id == id }), let url = trustedBrowser(target, strict: true) else {
        throw NSError(domain: "Select an installed, signed supported browser", code: 1)
    }
    let apps = NSWorkspace.shared.runningApplications.filter { $0.bundleIdentifier == id }
    guard !apps.contains(where: browserManaged) else { throw NSError(domain: "AO-managed or agent browsers cannot be relaunched", code: 2) }
    for app in apps where !app.isTerminated {
        guard app.terminate() else { throw NSError(domain: "The browser declined to quit; no force quit was attempted", code: 3) }
    }
    let until = ProcessInfo.processInfo.systemUptime + 5
    while apps.contains(where: { !$0.isTerminated }) && ProcessInfo.processInfo.systemUptime < until {
        RunLoop.current.run(until: Date().addingTimeInterval(0.05))
    }
    guard !apps.contains(where: { !$0.isTerminated }) else { throw NSError(domain: "Finish the browser's quit dialog before retrying", code: 4) }
    let configuration = NSWorkspace.OpenConfiguration()
    configuration.arguments = ["--disable-background-timer-throttling", "--disable-renderer-backgrounding", "--disable-backgrounding-occluded-windows"]
    let completion = WakeBrowserCompletion()
    NSWorkspace.shared.openApplication(at: url, configuration: configuration) { _, error in completion.finish(error == nil) }
    let deadline = ProcessInfo.processInfo.systemUptime + 5
    while completion.result == nil && ProcessInfo.processInfo.systemUptime < deadline { RunLoop.current.run(until: Date().addingTimeInterval(0.05)) }
    guard completion.result == true else { throw NSError(domain: "Browser launch could not be confirmed; check the browser before retrying", code: 5) }
}

private final class WakeBrowserCompletion: @unchecked Sendable {
    private let lock = NSLock()
    private var value: Bool?
    var result: Bool? { lock.lock(); defer { lock.unlock() }; return value }
    func finish(_ success: Bool) { lock.lock(); defer { lock.unlock() }; value = success }
}
