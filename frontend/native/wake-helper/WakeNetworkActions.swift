import Foundation
import CoreWLAN
import SystemConfiguration
import Darwin

func wakePrimaryInterface() -> String? {
    guard let store = SCDynamicStoreCreate(nil, "AO Awake" as CFString, nil, nil) else { return nil }
    for key in ["State:/Network/Global/IPv4", "State:/Network/Global/IPv6"] {
        if let state = SCDynamicStoreCopyValue(store, key as CFString) as? [String: Any], let name = state["PrimaryInterface"] as? String { return name }
    }
    return nil
}
func wakeNetworkConnected() -> Bool? {
    guard let store = SCDynamicStoreCreate(nil, "AO Awake" as CFString, nil, nil) else { return nil }
    var absent = 0
    for key in ["State:/Network/Global/IPv4", "State:/Network/Global/IPv6"] {
        if let value = SCDynamicStoreCopyValue(store, key as CFString) {
            guard let state = value as? [String: Any], let primary = state["PrimaryInterface"] as? String, !primary.isEmpty else { return nil }
            return true
        } else if SCError() == kSCStatusNoKey { absent += 1 } else { return nil }
    }
    return absent == 2 ? false : nil
}
func wakeKnownNetworks() -> [String] {
    guard let interface = CWWiFiClient.shared().interface(), interface.powerOn(),
          let config = interface.configuration(), !config.requireAdministratorForAssociation else { return [] }
    return config.networkProfiles.array.compactMap { ($0 as? CWNetworkProfile)?.ssid }.sorted()
}
func joinWakeKnownNetwork(_ ssid: String) throws {
    guard wakeNetworkConnected() == false else { throw NSError(domain: "The network already recovered; no handoff was attempted", code: 1) }
    guard wakeKnownNetworks().contains(ssid), let interface = CWWiFiClient.shared().interface(), let name = interface.interfaceName else {
        throw NSError(domain: "Configure this hotspot in macOS Wi-Fi settings and grant Wi-Fi access first", code: 2)
    }
    // Credentials remain managed by macOS. AO accepts only a saved network name;
    // no password is copied into argv, logs, SQLite or recovery files.
    let command = Process()
    command.executableURL = URL(fileURLWithPath: "/usr/sbin/networksetup")
    command.arguments = ["-setairportnetwork", name, ssid]
    command.standardOutput = FileHandle.nullDevice; command.standardError = FileHandle.nullDevice
    let owner = getppid(); let identity = processStart(owner)
    try command.run()
    let until = ProcessInfo.processInfo.systemUptime + 8
    while command.isRunning && ProcessInfo.processInfo.systemUptime < until {
        if identity == nil || processStart(owner) != identity { command.terminate(); break }
        if let primary = wakePrimaryInterface(), primary != name { command.terminate(); break }
        if let current = interface.ssid(), current != ssid { command.terminate(); break }
        usleep(100_000)
    }
    if command.isRunning { kill(command.processIdentifier, SIGKILL) }
    command.waitUntilExit()
    guard command.terminationStatus == 0, interface.ssid() == ssid else {
        throw NSError(domain: "Could not confirm the hotspot connection; check saved credentials and Wi-Fi permissions in macOS", code: 3)
    }
}
