// Regenerates every Orchestrator icon, logo and mascot file from the two
// sources in assets/brand (see assets/brand/README.md):
//   ruto.svg           the mark: paths baton-edge, baton, body (eyes cut out), eyes
//   AO.icon/icon.json  the Liquid Glass settings, an Icon Composer document
// Needs macOS 26+ with Xcode 26+ (ictool, actool). Run from anywhere:
//   swift scripts/generate-brand-assets.swift
// Outputs are committed. Never edit one by hand: change a source and rerun.
import AppKit
import Foundation
import ImageIO
import UniformTypeIdentifiers

let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
let fm = FileManager.default
let brand = root.appendingPathComponent("assets/brand")
let iconBundle = brand.appendingPathComponent("AO.icon")
let tmp = fm.temporaryDirectory.appendingPathComponent("ao-brand-\(ProcessInfo.processInfo.processIdentifier)")
try? fm.removeItem(at: tmp)
try fm.createDirectory(at: tmp, withIntermediateDirectories: true)
defer { try? fm.removeItem(at: tmp) }
let sRGB = CGColorSpace(name: CGColorSpace.sRGB)!

// MARK: - Tools

@discardableResult
func run(_ executable: String, _ arguments: [String]) -> String {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: executable)
    process.arguments = arguments
    let pipe = Pipe()
    process.standardOutput = pipe
    process.standardError = pipe
    try! process.run()
    let output = String(decoding: pipe.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
    process.waitUntilExit()
    guard process.terminationStatus == 0 else { fatalError("\(executable) \(arguments.joined(separator: " ")) failed:\n\(output)") }
    return output
}

func temporary(_ name: String) -> URL { tmp.appendingPathComponent(name) }

func write(_ data: Data, to relative: String) {
    let url = root.appendingPathComponent(relative)
    try! fm.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
    try! data.write(to: url)
    print("wrote \(relative)")
}

// MARK: - Source

let source = try String(contentsOf: brand.appendingPathComponent("ruto.svg"), encoding: .utf8)

func firstMatch(_ pattern: String) -> String {
    let regex = try! NSRegularExpression(pattern: pattern, options: [.dotMatchesLineSeparators])
    guard let match = regex.firstMatch(in: source, range: NSRange(source.startIndex..., in: source)) else {
        fatalError("assets/brand/ruto.svg does not match \(pattern)")
    }
    return String(source[Range(match.range, in: source)!])
}

/// A path from ruto.svg, without its id.
func element(_ id: String) -> String {
    firstMatch("<path id=\"\(id)\"[^>]*/>").replacingOccurrences(of: " id=\"\(id)\"", with: "")
}

func paint(_ element: String, _ attribute: String, _ color: String) -> String {
    element.replacingOccurrences(of: "\(attribute)=\"[^\"]*\"", with: "\(attribute)=\"\(color)\"", options: .regularExpression)
}

let defs = firstMatch("<defs>.*?</defs>")
let layerHeader = "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"1024\" height=\"1024\" viewBox=\"0 0 1024 1024\">"
// Icon Composer layers: the glass shows the tile through the eye holes.
let rutoLayer = layerHeader + defs + element("body") + "</svg>"
let batonLayer = layerHeader + element("baton") + "</svg>"
// Flat logo for the app UI, docs and site: the whole mark, cropped square.
func flat(viewBox: String) -> String {
    source
        .replacingOccurrences(of: "viewBox=\"0 0 1024 1024\"", with: "viewBox=\"\(viewBox)\"")
        .replacingOccurrences(of: " id=\"(baton-edge|baton|body|eyes)\"", with: "", options: .regularExpression)
}
let flatLogo = flat(viewBox: "121 88 820 820")
// The app's mascot slot (sidebar brand, startup, first run): Ruto fills 80% of
// the width, the optical size the brand row was designed around.
let appMascot = flat(viewBox: "31 -2 1000 1000")
// One-colour silhouette (eyes cut out) for template, monochrome and
// notification icons. The wider baton-edge keeps the baton visible small.
let silhouette = layerHeader + paint(element("baton-edge"), "stroke", "#000") + paint(element("body"), "fill", "#000") + "</svg>"

// MARK: - Drawing

func canvas(_ width: Int, _ height: Int, opaque: Bool = false) -> CGContext {
    let alpha = opaque ? CGImageAlphaInfo.noneSkipLast : CGImageAlphaInfo.premultipliedLast
    let context = CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4,
                            space: sRGB, bitmapInfo: alpha.rawValue)!
    context.interpolationQuality = .high
    return context
}

/// Draws an SVG document into a rect given in top-left coordinates.
func draw(svg: String, in context: CGContext, rect: CGRect) {
    let file = temporary("\(UUID().uuidString).svg")
    try! svg.write(to: file, atomically: true, encoding: .utf8)
    let image = NSImage(contentsOf: file)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(cgContext: context, flipped: false)
    image.draw(in: NSRect(x: rect.minX, y: CGFloat(context.height) - rect.maxY, width: rect.width, height: rect.height))
    NSGraphicsContext.restoreGraphicsState()
}

/// Draws an image into a rect given in top-left coordinates.
func draw(_ image: CGImage, in context: CGContext, rect: CGRect) {
    context.draw(image, in: CGRect(x: rect.minX, y: CGFloat(context.height) - rect.maxY, width: rect.width, height: rect.height))
}

func load(_ url: URL) -> CGImage {
    let image = CGImageSourceCreateImageAtIndex(CGImageSourceCreateWithURL(url as CFURL, nil)!, 0, nil)!
    let context = canvas(image.width, image.height)
    context.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
    return context.makeImage()!
}

/// Downscales in halving steps so small sizes stay sharp instead of aliased.
func resized(_ image: CGImage, _ width: Int, _ height: Int) -> CGImage {
    var current = image
    while current.width >= width * 4 && current.height >= height * 4 {
        let context = canvas(current.width / 2, current.height / 2)
        context.draw(current, in: CGRect(x: 0, y: 0, width: context.width, height: context.height))
        current = context.makeImage()!
    }
    let context = canvas(width, height)
    context.draw(current, in: CGRect(x: 0, y: 0, width: width, height: height))
    return context.makeImage()!
}

func pixels(_ image: CGImage) -> [UInt8] {
    let context = canvas(image.width, image.height)
    context.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
    let data = context.data!.assumingMemoryBound(to: UInt8.self)
    return Array(UnsafeBufferPointer(start: data, count: image.width * image.height * 4))
}

func image(from bytes: [UInt8], _ width: Int, _ height: Int) -> CGImage {
    var bytes = bytes
    return bytes.withUnsafeMutableBytes { buffer in
        CGContext(data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4,
                  space: sRGB, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!.makeImage()!
    }
}

/// Bounds of the pixels at or above an alpha, in top-left coordinates.
func opaqueBounds(_ image: CGImage, threshold: UInt8 = 8) -> CGRect {
    let data = pixels(image)
    var minX = image.width, minY = image.height, maxX = -1, maxY = -1
    for y in 0..<image.height {
        for x in 0..<image.width where data[(y * image.width + x) * 4 + 3] >= threshold {
            minX = min(minX, x); maxX = max(maxX, x); minY = min(minY, y); maxY = max(maxY, y)
        }
    }
    return CGRect(x: minX, y: minY, width: maxX - minX + 1, height: maxY - minY + 1)
}

/// Crops to the opaque pixels and centers them, aspect kept, in a box.
func fitted(_ image: CGImage, width: Int, height: Int, box: CGSize, threshold: UInt8 = 8) -> CGImage {
    let crop = image.cropping(to: opaqueBounds(image, threshold: threshold))!
    let scale = min(box.width / CGFloat(crop.width), box.height / CGFloat(crop.height))
    let size = CGSize(width: CGFloat(crop.width) * scale, height: CGFloat(crop.height) * scale)
    let context = canvas(width, height)
    draw(resized(crop, Int(size.width.rounded()), Int(size.height.rounded())), in: context,
         rect: CGRect(x: ((CGFloat(width) - size.width) / 2).rounded(), y: ((CGFloat(height) - size.height) / 2).rounded(),
                      width: size.width.rounded(), height: size.height.rounded()))
    return context.makeImage()!
}

func tinted(_ image: CGImage, _ color: CGColor) -> CGImage {
    let context = canvas(image.width, image.height)
    let rect = CGRect(x: 0, y: 0, width: image.width, height: image.height)
    context.draw(image, in: rect)
    context.setBlendMode(.sourceIn)
    context.setFillColor(color)
    context.fill(rect)
    return context.makeImage()!
}

func png(_ image: CGImage) -> Data {
    let data = NSMutableData()
    let destination = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil)!
    CGImageDestinationAddImage(destination, image, nil)
    CGImageDestinationFinalize(destination)
    return data as Data
}

func writePNG(_ image: CGImage, _ relative: String) { write(png(image), to: relative) }

// MARK: - Icon Composer

let document = try JSONSerialization.jsonObject(with: Data(contentsOf: iconBundle.appendingPathComponent("icon.json"))) as! [String: Any]

// The layer placement Icon Composer applies, shared by both layers.
let position: (scale: CGFloat, x: CGFloat, y: CGFloat) = {
    let groups = document["groups"] as! [[String: Any]]
    let layer = (groups[0]["layers"] as! [[String: Any]])[0]
    let position = layer["position"] as? [String: Any] ?? [:]
    let translation = position["translation-in-points"] as? [Double] ?? [0, 0]
    return (CGFloat(position["scale"] as? Double ?? 1), CGFloat(translation[0]), CGFloat(translation[1]))
}()

func color(_ value: String) -> CGColor {
    let parts = value.split(separator: ":")
    let components = parts[1].split(separator: ",").map { CGFloat(Double($0)!) }
    switch parts[0] {
    case "srgb", "extended-srgb": return CGColor(colorSpace: sRGB, components: components)!
    case "gray", "extended-gray": return CGColor(gray: components[0], alpha: components[1])
    default: fatalError("unsupported icon.json colour \(value)")
    }
}

/// The tile background from icon.json, full-bleed (no squircle).
func background(_ size: Int) -> CGImage {
    let context = canvas(size, size, opaque: true)
    let fill = document["fill"] as! [String: Any]
    let s = CGFloat(size)
    if let solid = fill["solid"] as? String {
        context.setFillColor(color(solid))
        context.fill(CGRect(x: 0, y: 0, width: s, height: s))
    } else if let stops = fill["linear-gradient"] as? [String], let orientation = fill["orientation"] as? [String: [String: Double]] {
        let gradient = CGGradient(colorsSpace: sRGB, colors: stops.map(color) as CFArray, locations: nil)!
        let point = { (key: String) in CGPoint(x: orientation[key]!["x"]! * s, y: s - orientation[key]!["y"]! * s) }
        context.drawLinearGradient(gradient, start: point("start"), end: point("stop"), options: [.drawsBeforeStartLocation, .drawsAfterEndLocation])
    } else {
        fatalError("unsupported icon.json fill \(fill)")
    }
    return context.makeImage()!
}

/// A copy of AO.icon with its fill replaced, for the foreground matte.
func bundle(named name: String, fill: String) -> URL {
    let copy = temporary("\(name).icon")
    try? fm.removeItem(at: copy)
    try! fm.copyItem(at: iconBundle, to: copy)
    var json = document
    json["fill"] = ["solid": fill]
    try! JSONSerialization.data(withJSONObject: json, options: [.prettyPrinted, .sortedKeys]).write(to: copy.appendingPathComponent("icon.json"))
    return copy
}

// Icon Composer's renderer. `xcrun ictool` is a different, asset catalog tool.
let iconComposerTool = URL(fileURLWithPath: run("/usr/bin/xcode-select", ["-p"]).trimmingCharacters(in: .whitespacesAndNewlines))
    .deletingLastPathComponent().appendingPathComponent("Applications/Icon Composer.app/Contents/Executables/ictool").path

/// A real Icon Composer render (squircle mask applied by ictool).
func ictool(_ icon: URL = iconBundle, platform: String, rendition: String = "Default", size: Int) -> CGImage {
    let output = temporary("\(UUID().uuidString).png")
    run(iconComposerTool, [icon.path, "--export-image", "--output-file", output.path, "--platform", platform,
                           "--rendition", rendition, "--width", "\(size)", "--height", "\(size)", "--scale", "1"])
    return load(output)
}

/// The glass-rendered mark alone on transparency, in the 1024 iOS layout.
/// ictool always paints a tile, so render it on black and on white and solve
/// for each pixel's alpha (difference matting); over black the colour is
/// already premultiplied. Pixels near the tile edge are dropped so the
/// tile's own rim highlight never leaks into the matte.
func glassForeground() -> CGImage {
    let size = 1024
    let black = pixels(ictool(bundle(named: "black", fill: "srgb:0.00000,0.00000,0.00000,1.00000"), platform: "iOS", size: size))
    let white = pixels(ictool(bundle(named: "white", fill: "srgb:1.00000,1.00000,1.00000,1.00000"), platform: "iOS", size: size))
    var out = [UInt8](repeating: 0, count: size * size * 4)
    for y in 96..<(size - 96) {
        for x in 96..<(size - 96) {
            let i = (y * size + x) * 4
            let spread = (Int(white[i]) - Int(black[i]) + Int(white[i + 1]) - Int(black[i + 1]) + Int(white[i + 2]) - Int(black[i + 2])) / 3
            let alpha = UInt8(max(0, min(255, 255 - spread)) * Int(black[i + 3]) / 255)
            out[i] = min(black[i], alpha); out[i + 1] = min(black[i + 1], alpha); out[i + 2] = min(black[i + 2], alpha); out[i + 3] = alpha
        }
    }
    return image(from: out, size, size)
}

/// An SVG drawn where Icon Composer places a layer, on a 1024 canvas.
func placed(_ svg: String) -> CGImage {
    let context = canvas(1024, 1024)
    let side = 1024 * position.scale
    draw(svg: svg, in: context, rect: CGRect(x: 512 - side / 2 + position.x, y: 512 - side / 2 + position.y, width: side, height: side))
    return context.makeImage()!
}

func ico(_ images: [CGImage]) -> Data {
    var header = Data([0, 0, 1, 0, UInt8(images.count), 0])
    var body = Data()
    let offset = 6 + 16 * images.count
    for image in images {
        let data = png(image)
        var entry = Data([UInt8(image.width % 256), UInt8(image.height % 256), 0, 0, 1, 0, 32, 0])
        withUnsafeBytes(of: UInt32(data.count).littleEndian) { entry.append(contentsOf: $0) }
        withUnsafeBytes(of: UInt32(offset + body.count).littleEndian) { entry.append(contentsOf: $0) }
        header.append(entry)
        body.append(data)
    }
    return header + body
}

// MARK: - Icon Composer bundle

write(Data(rutoLayer.utf8), to: "assets/brand/AO.icon/Assets/ruto.svg")
write(Data(batonLayer.utf8), to: "assets/brand/AO.icon/Assets/baton.svg")

// Expo builds the iOS icon from its own copy of the bundle.
let mobileIcon = root.appendingPathComponent("packages/mobile/assets/AO.icon")
try? fm.removeItem(at: mobileIcon)
try fm.copyItem(at: iconBundle, to: mobileIcon)
print("wrote packages/mobile/assets/AO.icon")

// macOS 26+: the compiled asset catalog the packaged app names in
// CFBundleIconName. Compiled here, not at package time, because actool's
// .icon support needs macOS 26 and release builds also run on older hosts.
// actool output differs byte-for-byte on every run, so it is only rebuilt
// when the bundle changes; Assets.car.sha256 records the bundle it came from.
do {
    let inputs = ["icon.json", "Assets/ruto.svg", "Assets/baton.svg"]
    let digest = inputs.map { input in
        String(run("/usr/bin/shasum", ["-a", "256", iconBundle.appendingPathComponent(input).path]).prefix(64)) + "  " + input + "\n"
    }.joined()
    let stamp = root.appendingPathComponent("frontend/assets/Assets.car.sha256")
    let car = root.appendingPathComponent("frontend/assets/Assets.car")
    if (try? String(contentsOf: stamp, encoding: .utf8)) == digest && fm.fileExists(atPath: car.path) {
        print("kept frontend/assets/Assets.car (AO.icon unchanged)")
    } else {
        let compileInput = temporary("AppIcon.icon")
        try fm.copyItem(at: iconBundle, to: compileInput)
        let compiled = temporary("compiled")
        try fm.createDirectory(at: compiled, withIntermediateDirectories: true)
        run("/usr/bin/xcrun", ["actool", compileInput.path, "--compile", compiled.path, "--output-format", "human-readable-text",
                               "--notices", "--warnings", "--output-partial-info-plist", temporary("partial.plist").path,
                               "--app-icon", "AppIcon", "--include-all-app-icons", "--enable-on-demand-resources", "NO",
                               "--development-region", "en", "--target-device", "mac", "--minimum-deployment-target", "26.0",
                               "--platform", "macosx"])
        write(try Data(contentsOf: compiled.appendingPathComponent("Assets.car")), to: "frontend/assets/Assets.car")
        write(Data(digest.utf8), to: "frontend/assets/Assets.car.sha256")
    }
}

// MARK: - Desktop

// macOS before 26 and every other platform get a pre-rendered icon: the
// Icon Composer render of the tile at the 824 pt macOS icon body, centered
// on the 1024 canvas with the standard drop shadow (mask baked in).
let desktop: CGImage = {
    let context = canvas(1024, 1024)
    context.setShadow(offset: CGSize(width: 0, height: -12), blur: 24, color: CGColor(gray: 0, alpha: 0.35))
    draw(ictool(platform: "macOS", size: 824), in: context, rect: CGRect(x: 100, y: 100, width: 824, height: 824))
    return context.makeImage()!
}()
writePNG(desktop, "frontend/assets/icon.png")

let iconset = temporary("icon.iconset")
try fm.createDirectory(at: iconset, withIntermediateDirectories: true)
for points in [16, 32, 128, 256, 512] {
    for scale in [1, 2] {
        let name = scale == 1 ? "icon_\(points)x\(points).png" : "icon_\(points)x\(points)@2x.png"
        try png(resized(desktop, points * scale, points * scale)).write(to: iconset.appendingPathComponent(name))
    }
}
run("/usr/bin/iconutil", ["-c", "icns", iconset.path, "-o", root.appendingPathComponent("frontend/assets/icon.icns").path])
print("wrote frontend/assets/icon.icns")

write(ico([16, 24, 32, 48, 64, 72, 96, 128, 256].map { resized(desktop, $0, $0) }), to: "frontend/assets/icon.ico")

// Unpackaged runs show an orange DEV badge so they never pass for the app.
do {
    let context = canvas(1024, 1024)
    draw(desktop, in: context, rect: CGRect(x: 0, y: 0, width: 1024, height: 1024))
    let badge = CGRect(x: 352, y: 798, width: 320, height: 124)
    context.setShadow(offset: CGSize(width: 0, height: -4), blur: 12, color: CGColor(gray: 0, alpha: 0.35))
    context.setFillColor(CGColor(srgbRed: 1, green: 0.66, blue: 0.13, alpha: 1))
    context.addPath(CGPath(roundedRect: CGRect(x: badge.minX, y: 1024 - badge.maxY, width: badge.width, height: badge.height),
                           cornerWidth: 28, cornerHeight: 28, transform: nil))
    context.fillPath()
    context.setShadow(offset: .zero, blur: 0, color: nil)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(cgContext: context, flipped: false)
    let text = NSAttributedString(string: "DEV", attributes: [
        .font: NSFont.systemFont(ofSize: 92, weight: .black),
        .foregroundColor: NSColor(srgbRed: 0.08, green: 0.06, blue: 0.02, alpha: 1),
        .kern: 4,
    ])
    let size = text.size()
    text.draw(at: NSPoint(x: badge.midX - size.width / 2, y: 1024 - badge.midY - size.height / 2))
    NSGraphicsContext.restoreGraphicsState()
    writePNG(context.makeImage()!, "frontend/assets/icon-dev.png")
}

for size in [16, 32, 48, 128] {
    writePNG(resized(desktop, size, size), "frontend/src/browser-extension/icons/icon-\(size).png")
}

// The flat logo: docs OG images and the site's copy (assets/ao-logo.svg,
// mirrored to orchestrator.inc by hand). The app UI uses ao-mascot.svg.
write(Data(flatLogo.utf8), to: "frontend/assets/ao-logo.svg")
write(Data(flatLogo.utf8), to: "assets/ao-logo.svg")
write(Data(appMascot.utf8), to: "frontend/assets/ao-mascot.svg")

// QR code centre: drawn on white paper, so the flat logo without a tile.
do {
    let context = canvas(1024, 1024)
    draw(svg: flatLogo, in: context, rect: CGRect(x: 0, y: 0, width: 1024, height: 1024))
    let logo = context.makeImage()!
    let bounds = opaqueBounds(logo)
    let height = Int((160 * bounds.height / bounds.width).rounded())
    writePNG(fitted(logo, width: 160, height: height, box: CGSize(width: 160, height: height)), "frontend/src/renderer/assets/ao-qr-logo.png")
}

// Menu bar template icons, built by their own script from the silhouette.
do {
    let context = canvas(1024, 1024)
    draw(svg: silhouette, in: context, rect: CGRect(x: 0, y: 0, width: 1024, height: 1024))
    let file = temporary("silhouette.png")
    try png(context.makeImage()!).write(to: file)
    print(run("/usr/bin/xcrun", ["swift", root.appendingPathComponent("frontend/scripts/generate-awake-tray-icon.swift").path,
                                 root.appendingPathComponent("frontend/assets").path, file.path]), terminator: "")
    print("wrote frontend/assets/tray*Template*.png")
}

// MARK: - README

writePNG(resized(ictool(platform: "macOS", size: 1024), 192, 192), "docs/assets/readme/logo.png")

// MARK: - Mobile

let foreground = glassForeground()
let white = CGColor(gray: 1, alpha: 1)

// iOS uses AO.icon; this full-bleed square (no rounding, no alpha) is Expo's
// fallback icon. The system applies its own mask.
do {
    let context = canvas(1024, 1024, opaque: true)
    draw(background(1024), in: context, rect: CGRect(x: 0, y: 0, width: 1024, height: 1024))
    draw(foreground, in: context, rect: CGRect(x: 0, y: 0, width: 1024, height: 1024))
    writePNG(context.makeImage()!, "packages/mobile/assets/icon.png")
}

// Android adaptive icon: 108 dp layers, of which 72 dp show through the mask,
// so the iOS layout shrinks by 72/108 to keep Ruto inside the 66 dp safe zone.
let adaptive: CGFloat = 72.0 / 108.0
let adaptiveRect = CGRect(x: 512 - 512 * adaptive, y: 512 - 512 * adaptive, width: 1024 * adaptive, height: 1024 * adaptive)
writePNG(background(1024), "packages/mobile/assets/android-icon-background.png")
do {
    let context = canvas(1024, 1024)
    draw(foreground, in: context, rect: adaptiveRect)
    writePNG(context.makeImage()!, "packages/mobile/assets/android-icon-foreground.png")
}
do {
    let context = canvas(1024, 1024)
    draw(tinted(placed(silhouette), white), in: context, rect: adaptiveRect)
    writePNG(context.makeImage()!, "packages/mobile/assets/android-icon-monochrome.png")
}

// Android status bar: white silhouette on transparency, 96 px (xxxhdpi 24 dp).
writePNG(fitted(tinted(placed(silhouette), white), width: 96, height: 96, box: CGSize(width: 84, height: 84)), "packages/mobile/assets/notification-icon.png")

writePNG(fitted(foreground, width: 1024, height: 1024, box: CGSize(width: 760, height: 760)), "packages/mobile/assets/splash-icon.png")
writePNG(resized(ictool(platform: "iOS", size: 1024), 256, 256), "packages/mobile/assets/favicon.png")
// Cropped to the artwork, not its glow. lib/ui.tsx MASCOT_ASPECT is 200 / 256,
// and WAND_TIP_X/Y there locate the baton tip in this crop.
// The eyes are painted in, as in the flat logo, so they stay dark on light themes.
do {
    let context = canvas(1024, 1024)
    draw(placed(layerHeader + element("eyes") + "</svg>"), in: context, rect: CGRect(x: 0, y: 0, width: 1024, height: 1024))
    draw(foreground, in: context, rect: CGRect(x: 0, y: 0, width: 1024, height: 1024))
    writePNG(fitted(context.makeImage()!, width: 256, height: 200, box: CGSize(width: 256, height: 200), threshold: 128), "packages/mobile/assets/mascot.png")
}
