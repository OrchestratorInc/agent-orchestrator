// Private API bridge declarations informed by Insomnia's DisplayPower.swift.
// Copyright (c) 2026 Krish Garg. MIT notice: THIRD_PARTY_NOTICES.md.
// AO's ownership and restoration policy is independent of that implementation.
import Foundation
import AppKit
import CoreGraphics
import ObjectiveC
import Darwin

@objc private protocol AwakeKeyboardAPI: NSObjectProtocol {
    @objc(copyKeyboardBacklightIDs) func ids() -> NSArray?
    @objc(isKeyboardBuiltIn:) func builtIn(_ id: UInt64) -> Bool
    @objc(brightnessForKeyboard:) func brightness(_ id: UInt64) -> Float
    @objc(setBrightness:forKeyboard:) func set(_ value: Float, _ id: UInt64) -> Bool
    @objc(isBacklightSuppressedOnKeyboard:) func suppressed(_ id: UInt64) -> Bool
    @objc(isBacklightDimmedOnKeyboard:) func dimmed(_ id: UInt64) -> Bool
}

struct WakeLightChange: Codable {
    let kind: String
    let identity: String
    let boot: String
    let original: Float
}

protocol WakeLightDriver {
    var boot: String { get }
    func targets(_ kind: String) -> [String]
    func value(_ kind: String, _ identity: String, trusted: Bool) -> Float?
    func set(_ kind: String, _ identity: String, _ value: Float) -> Bool
    func original(_ kind: String, _ identity: String) -> Float?
    func sampleOpen()
}
extension WakeLightDriver {
    func value(_ kind: String, _ identity: String) -> Float? { value(kind, identity, trusted: false) }
}
final class WakeLights: WakeLightDriver {
    private typealias ReadDisplay = @convention(c) (CGDirectDisplayID, UnsafeMutablePointer<Float>) -> Int32
    private typealias SetDisplay = @convention(c) (CGDirectDisplayID, Float) -> Int32
    private typealias CanDisplay = @convention(c) (CGDirectDisplayID) -> Bool
    private var displayGet: ReadDisplay?
    private var displaySet: SetDisplay?
    private var displayCan: CanDisplay?
    private var keyboard: (any AwakeKeyboardAPI)?
    private var lastOpen: [String: (Float, Double)] = [:]
    let boot: String
    init() {
        var size = 0
        if sysctlbyname("kern.bootsessionuuid", nil, &size, nil, 0) == 0, size > 0, size < 256 {
            var bytes = [CChar](repeating: 0, count: size)
            if sysctlbyname("kern.bootsessionuuid", &bytes, &size, nil, 0) == 0 { boot = String(cString: bytes) } else { boot = "" }
        } else { boot = "" }
        guard WakeLightPolicy.supportsOS(ProcessInfo.processInfo.operatingSystemVersion.majorVersion), !boot.isEmpty else { return }
        // C symbols have no discoverable ABI; restrict them to the measured OS
        // range. Keyboard selectors additionally require matching type encodings.
        if let handle = dlopen("/System/Library/PrivateFrameworks/DisplayServices.framework/DisplayServices", RTLD_NOW | RTLD_LOCAL),
           let get = dlsym(handle, "DisplayServicesGetBrightness"),
           let set = dlsym(handle, "DisplayServicesSetBrightness"),
           let can = dlsym(handle, "DisplayServicesCanChangeBrightness") {
            displayGet = unsafeBitCast(get, to: ReadDisplay.self)
            displaySet = unsafeBitCast(set, to: SetDisplay.self)
            displayCan = unsafeBitCast(can, to: CanDisplay.self)
        }
        if dlopen("/System/Library/PrivateFrameworks/CoreBrightness.framework/CoreBrightness", RTLD_NOW | RTLD_LOCAL) != nil,
           let cls = NSClassFromString("KeyboardBrightnessClient"), let type = cls as? NSObject.Type {
            var encodings: [String: String] = [:]
            for name in WakeLightPolicy.keyboardSignatures.keys {
                if let method = class_getInstanceMethod(cls, NSSelectorFromString(name)), let value = method_getTypeEncoding(method) {
                    encodings[name] = String(cString: value)
                }
            }
            if WakeLightPolicy.keyboardCompatible(encodings) {
                keyboard = unsafeBitCast(type.init(), to: (any AwakeKeyboardAPI).self)
            }
        }
    }
    private func display() -> CGDirectDisplayID? {
        var ids = [CGDirectDisplayID](repeating: 0, count: 32); var count: UInt32 = 0
        guard CGGetOnlineDisplayList(UInt32(ids.count), &ids, &count) == .success else { return nil }
        return ids.prefix(Int(count)).first { CGDisplayIsBuiltin($0) != 0 && displayCan?($0) == true }
    }
    private func displayIdentity(_ id: CGDirectDisplayID) -> String? {
        guard let uuid = CGDisplayCreateUUIDFromDisplayID(id)?.takeRetainedValue() else { return nil }
        return CFUUIDCreateString(nil, uuid) as String
    }
    private func keyboards() -> [UInt64] {
        guard let keyboard else { return [] }
        return (keyboard.ids() as? [NSNumber] ?? []).map(\.uint64Value).filter { keyboard.builtIn($0) }
    }
    func targets(_ kind: String) -> [String] {
        if kind == "display", let id = display(), let identity = displayIdentity(id) { return [identity] }
        if kind == "keyboard" { return keyboards().map(String.init) }
        return []
    }
    func value(_ kind: String, _ identity: String, trusted: Bool = false) -> Float? {
        if kind == "display", let id = display(), displayIdentity(id) == identity, let get = displayGet {
            var value: Float = -1
            guard get(id, &value) == 0, value.isFinite, (0...1).contains(value) else { return nil }
            return value
        }
        if kind == "keyboard", let keyboard, let id = UInt64(identity), keyboards().contains(id) {
            if trusted && (keyboard.suppressed(id) || keyboard.dimmed(id)) { return nil }
            let value = keyboard.brightness(id)
            return value.isFinite && (0...1).contains(value) ? value : nil
        }
        return nil
    }
    func set(_ kind: String, _ identity: String, _ value: Float) -> Bool {
        guard value.isFinite, (0...1).contains(value) else { return false }
        if kind == "display", let id = display(), displayIdentity(id) == identity, let set = displaySet { return set(id, value) == 0 }
        if kind == "keyboard", let keyboard, let id = UInt64(identity), keyboards().contains(id) { return keyboard.set(value, id) }
        return false
    }
    func available(_ kind: String) -> Bool {
        return targets(kind).contains { value(kind, $0) != nil }
    }
    func sampleOpen() {
        let now = ProcessInfo.processInfo.systemUptime
        for kind in ["display", "keyboard"] {
            for identity in targets(kind) {
                if let value = value(kind, identity, trusted: true), value > 0 {
                    lastOpen[kind + identity] = (value, now)
                } else { lastOpen.removeValue(forKey: kind + identity) }
            }
        }
    }
    func original(_ kind: String, _ identity: String) -> Float? {
        guard let entry = lastOpen[kind + identity], ProcessInfo.processInfo.systemUptime - entry.1 < 10 else { return nil }
        return entry.0
    }
}
