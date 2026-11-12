// Regenerates every Orchestrator icon, logo and mascot file from two sources
// (see assets/brand/README.md):
//   the Ruto section below      the mark's geometry and shading; written out
//                               as assets/brand/ruto.svg
//   assets/brand/AO.icon/icon.json  the Liquid Glass settings, an Icon Composer document
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

// MARK: - Ruto

// Ruto as drawn on the pre-glass desktop icon, traced onto its 1024 grid
// (that icon's tile spans x 104-918). The rims are mitred chamfers built from
// the outline: every edge gets an outer line, a band and an inner ridge,
// coloured by which way the edge faces. Writes assets/brand/ruto.svg.

typealias Point = (x: Double, y: Double)

func arc(_ cx: Double, _ cy: Double, _ r: Double, _ from: Double, _ to: Double) -> [Point] {
    let steps = max(2, Int(abs(to - from) / 7.5))
    return (0...steps).map { i in
        let angle = (from + (to - from) * Double(i) / Double(steps)) * .pi / 180
        return (cx + r * cos(angle), cy + r * sin(angle))
    }
}

func legBottom(_ left: Double, _ right: Double) -> [Point] {
    arc(right - 7, 756, 7, 0, 90) + arc(left + 7, 756, 7, 90, 180)
}

let outline: [Point] = arc(323, 352, 28, 180, 270) + arc(663, 352, 28, 270, 360)
    + [(691, 476), (727, 476)] // shoulder, then the raised arm
    + arc(751, 416, 6, 195, 286) + arc(804, 432, 6, 286, 374)
    + [(787, 529), (745, 561), (691, 561)]
    + legBottom(628, 691) + [(628, 698), (591, 698)]
    + legBottom(528, 591) + [(528, 698), (459, 698)]
    + legBottom(394, 459) + [(394, 698), (357, 698)]
    + legBottom(295, 357)
    + [(295, 571), (272, 571), (272, 598), (281, 605)] // trunk
    + arc(275, 655, 6, 0, 90) + [(241, 661), (207, 627)]
    + arc(221, 490, 14, 180, 270) + [(295, 476)]
let pupils: [CGRect] = [CGRect(x: 381, y: 437, width: 43, height: 48), CGRect(x: 568, y: 437, width: 43, height: 48)]
// The baton: round cap centre, tilt in degrees, width and length into the hand.
let baton = (x: 835.5, y: 236.0, angle: 16.95, width: 17.5, length: 218.0)
// Everything Ruto covers, for crops.
let markBounds = CGRect(x: 207, y: 227, width: 637, height: 536)

typealias Bands = [(from: Double, to: Double, up: String, left: String, right: String, down: String)]
let bodyBands: Bands = [
    (0, 1.5, "#B6F0FF", "#68CCF7", "#0A8CD2", "#0981C2"),
    (1.5, 8, "#9CDEFA", "#5FC4F7", "#0B80CA", "#0C78BC"),
    (8, 10, "#D2F2FE", "#A0E0FE", "#31B6FC", "#34B5F8"),
]
// The eye walls slope down into the dark pupils.
let eyeBands: Bands = [
    (0, 8, "#88DCFD", "#6CCCFC", "#1E94D8", "#1484C8"),
    (8, 10, "#B7EBFE", "#B7EEFE", "#2FA1E2", "#31A3E2"),
]

func number(_ value: Double) -> String {
    let text = String(format: "%.1f", value)
    return text.hasSuffix(".0") ? String(text.dropLast(2)) : text
}

func pathData(_ points: [Point]) -> String {
    "M" + points.map { "\(number($0.x)) \(number($0.y))" }.joined(separator: "L") + "Z"
}

func signedArea(_ points: [Point]) -> Double {
    zip(points, points.dropFirst() + [points[0]]).reduce(0) { $0 + $1.0.x * $1.1.y - $1.1.x * $1.0.y } / 2
}

/// Drops repeated points; orients outlines clockwise on screen and holes the other way.
func contour(_ points: [Point], hole: Bool) -> [Point] {
    var out: [Point] = []
    for point in points where out.last.map({ hypot(point.x - $0.x, point.y - $0.y) > 0.5 }) ?? true { out.append(point) }
    if let first = out.first, let last = out.last, hypot(first.x - last.x, first.y - last.y) < 0.5 { out.removeLast() }
    return (signedArea(out) < 0) != hole ? out.reversed() : out
}

/// The outward normal of each edge.
func normals(_ points: [Point]) -> [Point] {
    points.indices.map { i in
        let a = points[i], b = points[(i + 1) % points.count]
        let length = hypot(b.x - a.x, b.y - a.y)
        return ((b.y - a.y) / length, (a.x - b.x) / length)
    }
}

/// The contour moved inward by a depth, corners mitred.
func inset(_ points: [Point], _ normals: [Point], _ depth: Double) -> [Point] {
    points.indices.map { i in
        let a = normals[(i + normals.count - 1) % normals.count], b = normals[i]
        let k = 1 + a.x * b.x + a.y * b.y
        return (points[i].x - depth * (a.x + b.x) / k, points[i].y - depth * (a.y + b.y) / k)
    }
}

/// The band colour for an edge facing `normal`, blended between the four
/// keyed directions in a few steps so neighbouring edges share paths.
func shade(_ normal: Point, _ band: (from: Double, to: Double, up: String, left: String, right: String, down: String)) -> String {
    let keys = [band.right, band.down, band.left, band.up] // screen angles 0, 90, 180, 270
    var angle = atan2(normal.y, normal.x) * 180 / .pi
    if angle < 0 { angle += 360 }
    let index = min(3, Int(angle / 90))
    let t = ((angle - Double(index) * 90) / 90 * 6).rounded() / 6
    let rgb = { (hex: String) in stride(from: 1, to: 7, by: 2).map { Double(Int(hex.dropFirst($0).prefix(2), radix: 16)!) } }
    let a = rgb(keys[index]), b = rgb(keys[(index + 1) % 4])
    return "#" + (0..<3).map { String(format: "%02X", Int((a[$0] + (b[$0] - a[$0]) * t).rounded())) }.joined()
}

func bevel(_ contours: [([Point], Bands)]) -> String {
    var paths: [String: String] = [:]
    for (points, bands) in contours {
        let normal = normals(points)
        var rings: [Double: [Point]] = [:]
        for band in bands { rings[band.from] = inset(points, normal, band.from); rings[band.to] = inset(points, normal, band.to) }
        for i in points.indices {
            let j = (i + 1) % points.count
            for band in bands {
                let outer = rings[band.from]!, inner = rings[band.to]!
                paths[shade(normal[i], band), default: ""] += pathData([outer[i], outer[j], inner[j], inner[i]])
            }
        }
    }
    return paths.keys.sorted().map { "<path fill=\"\($0)\" stroke=\"\($0)\" stroke-width=\".5\" stroke-linejoin=\"round\" d=\"\(paths[$0]!)\"/>" }.joined()
}

let rutoOutline = contour(outline, hole: false)
let eyeHoles = pupils.map { contour([(Double($0.minX), Double($0.minY)), (Double($0.minX), Double($0.maxY)),
                                     (Double($0.maxX), Double($0.maxY)), (Double($0.maxX), Double($0.minY))], hole: true) }
let outlineData = ([rutoOutline] + eyeHoles).map(pathData).joined()

func batonRect(_ fill: String, from top: Double = -baton.width / 2, extra: String = "") -> String {
    "<rect x=\"\(number(-baton.width / 2))\" y=\"\(number(top))\" width=\"\(number(baton.width))\" height=\"\(number(baton.length - top))\""
        + " rx=\"\(number(baton.width / 2))\" fill=\"\(fill)\"\(extra) transform=\"translate(\(number(baton.x)) \(number(baton.y))) rotate(\(baton.angle))\"/>"
}

let rutoDefs = "<defs>"
    + "<path id=\"ruto-outline\" fill-rule=\"evenodd\" d=\"\(outlineData)\"/>"
    + "<clipPath id=\"ruto-clip\"><use href=\"#ruto-outline\"/></clipPath>"
    + "<linearGradient id=\"ruto-face\" x1=\"300\" y1=\"330\" x2=\"520\" y2=\"779\" gradientUnits=\"userSpaceOnUse\"><stop offset=\"0\" stop-color=\"#76CEFE\"/><stop offset=\".5\" stop-color=\"#43B8F9\"/><stop offset=\".75\" stop-color=\"#32B1F7\"/><stop offset=\"1\" stop-color=\"#2AACF4\"/></linearGradient>"
    + "<radialGradient id=\"ruto-corner\" cx=\"312\" cy=\"342\" r=\"110\" gradientUnits=\"userSpaceOnUse\"><stop offset=\"0\" stop-color=\"#fff\" stop-opacity=\".2\"/><stop offset=\"1\" stop-color=\"#fff\" stop-opacity=\"0\"/></radialGradient>"
    + "<linearGradient id=\"ruto-pupil\" x2=\"1\" y2=\"1\"><stop offset=\"0\" stop-color=\"#090A0B\"/><stop offset=\"1\" stop-color=\"#1A1B1C\"/></linearGradient>"
    + "<linearGradient id=\"ruto-stick\" x2=\"1\"><stop offset=\"0\" stop-color=\"#414142\"/><stop offset=\".07\" stop-color=\"#EEECEE\"/><stop offset=\".3\" stop-color=\"#FEFDFC\"/><stop offset=\".55\" stop-color=\"#F2EEEA\"/><stop offset=\".75\" stop-color=\"#E7E4DE\"/><stop offset=\".87\" stop-color=\"#D5D2CA\"/><stop offset=\".94\" stop-color=\"#BBB5B0\"/><stop offset=\"1\" stop-color=\"#90908C\"/></linearGradient>"
    + "<linearGradient id=\"ruto-sunk\" y2=\"1\" x2=\"0\"><stop offset=\"0\" stop-color=\"#fff\" stop-opacity=\".55\"/><stop offset=\"1\" stop-color=\"#fff\" stop-opacity=\"0\"/></linearGradient>"
    + "</defs>"
let batonMarkup = batonRect("url(#ruto-stick)")
// Flat artwork shades the baton warm grey, like the old mascot, so it still
// reads on light surfaces. The app icon keeps ruto-stick on its dark tile.
let flatBaton = "<defs>"
    // Bright on the upper side, dimming smoothly across the stick to the lower side.
    + "<linearGradient id=\"ruto-stick-flat\" x2=\"1\"><stop offset=\"0\" stop-color=\"#FFFFFF\"/><stop offset=\".5\" stop-color=\"#EEE7E0\"/><stop offset=\"1\" stop-color=\"#BBADA2\"/></linearGradient>"
    + "</defs>"
    + batonRect("url(#ruto-stick-flat)")
let bodyMarkup = pupils.map { "<rect x=\"\(Int($0.minX))\" y=\"\(Int($0.minY))\" width=\"\(Int($0.width))\" height=\"\(Int($0.height))\" fill=\"url(#ruto-pupil)\"/>" }.joined()
    + "<use href=\"#ruto-outline\" fill=\"url(#ruto-face)\"/>"
    + "<rect x=\"200\" y=\"300\" width=\"300\" height=\"250\" fill=\"url(#ruto-corner)\" clip-path=\"url(#ruto-clip)\"/>"
    + bevel([(rutoOutline, bodyBands)] + eyeHoles.map { ($0, eyeBands) })
    // the baton shows faintly through the top of the hand
    + "<g clip-path=\"url(#ruto-clip)\">" + batonRect("url(#ruto-sunk)", from: 168, extra: " opacity=\".8\"") + "</g>"

let layerHeader = "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"1024\" height=\"1024\" viewBox=\"0 0 1024 1024\">"
let source = layerHeader + rutoDefs + "<g id=\"baton\">" + flatBaton + "</g><g id=\"body\">" + bodyMarkup + "</g></svg>"

// Icon Composer layers, one glass group each.
let rutoLayer = layerHeader + rutoDefs + bodyMarkup + "</svg>"
let batonLayer = layerHeader + rutoDefs + batonMarkup + "</svg>"
/// The whole mark, cropped to a square around Ruto with `fill` of its width.
func flat(fill: CGFloat) -> String {
    let side = markBounds.width / fill
    let viewBox = [markBounds.midX - side / 2, markBounds.midY - side / 2, side, side].map { number(Double($0)) }.joined(separator: " ")
    return source.replacingOccurrences(of: "viewBox=\"0 0 1024 1024\"", with: "viewBox=\"\(viewBox)\"")
        .replacingOccurrences(of: " width=\"1024\" height=\"1024\"", with: "")
}
// Flat logo for docs and the site: a tight square.
let flatLogo = flat(fill: 0.92)
// The app's mascot slot (sidebar brand, startup, first run): Ruto fills 80% of
// the width, the optical size the brand row was designed around.
let appMascot = flat(fill: 0.8)
// One-colour silhouette (eyes cut out) for template, monochrome and
// notification icons. The baton is drawn thicker so it survives small sizes.
let silhouette = layerHeader + "<path fill=\"#000\" fill-rule=\"evenodd\" d=\"\(outlineData)\"/>"
    + batonRect("#000", extra: " stroke=\"#000\" stroke-width=\"12\"") + "</svg>"

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

write(Data(source.utf8), to: "assets/brand/ruto.svg")
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

// Android adaptive icon: 108 dp layers, masked to any shape that keeps a
// centred 66 dp circle. Ruto is scaled down from the iOS layout until its
// farthest pixel sits 32 dp from the centre.
func farthest(_ image: CGImage) -> CGFloat {
    let data = pixels(image)
    var radius: CGFloat = 0
    for y in 0..<image.height {
        for x in 0..<image.width where data[(y * image.width + x) * 4 + 3] >= 128 {
            radius = max(radius, hypot(CGFloat(x) + 0.5 - CGFloat(image.width) / 2, CGFloat(y) + 0.5 - CGFloat(image.height) / 2))
        }
    }
    return radius
}
let adaptive = min(72.0 / 108.0, 32.0 / 108.0 * 1024 / max(farthest(foreground), farthest(placed(silhouette))))
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
// The flat artwork, like the desktop app's mascot: the glass render loses the
// baton's edge and looks washed out on light themes. lib/ui.tsx MASCOT_ASPECT
// is 200 / 256, and WAND_TIP_X/Y there locate the baton tip in this crop.
do {
    let context = canvas(1024, 1024)
    draw(svg: source, in: context, rect: CGRect(x: 0, y: 0, width: 1024, height: 1024))
    writePNG(fitted(context.makeImage()!, width: 256, height: 200, box: CGSize(width: 256, height: 200), threshold: 128), "packages/mobile/assets/mascot.png")
}
