// Run from the repository root:
// swift frontend/scripts/generate-awake-tray-icon.swift frontend/assets
// Builds both menu bar template icons from the current mascot (ao-mascot.png)
// so the mascot and the Lucide Coffee cup share one optical vertical center.
// Cup icon license: frontend/assets/trayAwakeIcon.LICENSE.
import AppKit

let assets = URL(fileURLWithPath: CommandLine.arguments[1])
let height = 20.0
let mascotMaxWidth = 18.0
let mascotMaxHeight = 16.0
let mascotLeft = 2.0
let cupSize = 13.0
let gap = 3.0

let source = NSImage(contentsOf: assets.appendingPathComponent("ao-mascot.png"))!
var proposed = NSRect(origin: .zero, size: source.size)
let cg = source.cgImage(forProposedRect: &proposed, context: nil, hints: nil)!

// Crop to the opaque body so the transparent padding in the artwork cannot
// shift the mascot off center.
func opaqueBounds(of image: CGImage) -> CGRect {
    let w = image.width, h = image.height
    var data = [UInt8](repeating: 0, count: w * h * 4)
    let ctx = CGContext(data: &data, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
        space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
    var minX = w, minY = h, maxX = -1, maxY = -1
    for y in 0..<h { for x in 0..<w where data[(y * w + x) * 4 + 3] > 40 {
        minX = min(minX, x); maxX = max(maxX, x); minY = min(minY, y); maxY = max(maxY, y)
    } }
    return CGRect(x: minX, y: minY, width: maxX - minX + 1, height: maxY - minY + 1)
}

let crop = opaqueBounds(of: cg)
let mascot = cg.cropping(to: crop)!
// Fit inside the box without distortion; the mascot is wider than tall.
let fit = min(mascotMaxWidth / Double(crop.width), mascotMaxHeight / Double(crop.height))
let mascotWidth = Double(crop.width) * fit
let mascotHeight = Double(crop.height) * fit

// Lucide Coffee ink bounds in its 24-unit box, stroke included.
let cupInk = CGRect(x: 2, y: 1, width: 20, height: 21)
let cupScale = cupSize / 24

func render(width: Double, scale: Int, cup: Bool, to name: String) throws {
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(width) * scale,
        pixelsHigh: Int(height) * scale, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
        isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    let context = NSGraphicsContext(bitmapImageRep: bitmap)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = context
    let c = context.cgContext
    c.scaleBy(x: CGFloat(scale), y: CGFloat(scale))

    // Mascot: flat black silhouette (eyes stay transparent), centered on the canvas.
    let rect = CGRect(x: cup ? mascotLeft : (width - mascotWidth) / 2,
        y: (height - mascotHeight) / 2, width: mascotWidth, height: mascotHeight)
    c.saveGState()
    c.interpolationQuality = .high
    c.draw(mascot, in: rect)
    c.setBlendMode(.sourceIn)
    c.setFillColor(NSColor.black.cgColor)
    c.fill(rect)
    c.restoreGState()

    if cup {
        // Place the cup's ink box, not its 24-unit frame, at the vertical center.
        let originX = mascotLeft + mascotWidth + gap - cupInk.minX * cupScale
        let originY = height / 2 + (cupInk.minY + cupInk.height / 2) * cupScale
        c.translateBy(x: originX, y: originY)
        c.scaleBy(x: cupScale, y: -cupScale)
        NSColor.black.setStroke()
        let path = NSBezierPath()
        path.lineWidth = 2
        path.lineCapStyle = .round
        path.lineJoinStyle = .round
        for x in [6.0, 10.0, 14.0] {
            path.move(to: NSPoint(x: x, y: 2))
            path.line(to: NSPoint(x: x, y: 4))
        }
        // M16 8a1 1 0 0 1 1 1v8a4 4 0 0 1-4 4H7a4 4 0 0 1-4-4V9
        // a1 1 0 0 1 1-1h14a4 4 0 1 1 0 8h-1
        path.move(to: NSPoint(x: 16, y: 8))
        path.appendArc(withCenter: NSPoint(x: 16, y: 9), radius: 1, startAngle: 270, endAngle: 360)
        path.line(to: NSPoint(x: 17, y: 17))
        path.appendArc(withCenter: NSPoint(x: 13, y: 17), radius: 4, startAngle: 0, endAngle: 90)
        path.line(to: NSPoint(x: 7, y: 21))
        path.appendArc(withCenter: NSPoint(x: 7, y: 17), radius: 4, startAngle: 90, endAngle: 180)
        path.line(to: NSPoint(x: 3, y: 9))
        path.appendArc(withCenter: NSPoint(x: 4, y: 9), radius: 1, startAngle: 180, endAngle: 270)
        path.line(to: NSPoint(x: 18, y: 8))
        path.appendArc(withCenter: NSPoint(x: 18, y: 12), radius: 4, startAngle: 270, endAngle: 450)
        path.line(to: NSPoint(x: 17, y: 16))
        path.stroke()
    }
    NSGraphicsContext.restoreGraphicsState()
    try bitmap.representation(using: .png, properties: [:])!.write(to: assets.appendingPathComponent(name))
}

let awakeWidth = ceil(mascotLeft + mascotWidth + gap + cupInk.width * cupScale + 2)
for scale in [1, 2] {
    let suffix = scale == 1 ? "" : "@2x"
    try render(width: 20, scale: scale, cup: false, to: "trayIconTemplate\(suffix).png")
    try render(width: awakeWidth, scale: scale, cup: true, to: "trayAwakeIconTemplate\(suffix).png")
}
