// Run from the repository root:
// swift frontend/scripts/generate-awake-tray-icon.swift frontend/assets
// Match the renderer's Lucide Coffee (24-unit viewBox, 2-unit round stroke).
// Icon license: frontend/assets/trayAwakeIcon.LICENSE.
import AppKit

let assets = URL(fileURLWithPath: CommandLine.arguments[1])
for scale in [1, 2] {
    let suffix = scale == 1 ? "" : "@2x"
    let source = NSImage(contentsOf: assets.appendingPathComponent("trayIconTemplate\(suffix).png"))!
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 40 * scale,
        pixelsHigh: 20 * scale, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
        isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    let context = NSGraphicsContext(bitmapImageRep: bitmap)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = context
    context.cgContext.scaleBy(x: CGFloat(scale), y: CGFloat(scale))
    source.draw(in: NSRect(x: 0, y: 0, width: 20, height: 20))
    // A 16-point cup, centered vertically, with a 3-point gap after AO.
    context.cgContext.translateBy(x: 23, y: 18)
    context.cgContext.scaleBy(x: 16.0 / 24, y: -16.0 / 24)
    NSColor.black.setStroke()
    let cup = NSBezierPath()
    cup.lineWidth = 2
    cup.lineCapStyle = .round
    cup.lineJoinStyle = .round
    for x in [6.0, 10.0, 14.0] {
        cup.move(to: NSPoint(x: x, y: 2))
        cup.line(to: NSPoint(x: x, y: 4))
    }
    // M16 8a1 1 0 0 1 1 1v8a4 4 0 0 1-4 4H7a4 4 0 0 1-4-4V9
    // a1 1 0 0 1 1-1h14a4 4 0 1 1 0 8h-1
    cup.move(to: NSPoint(x: 16, y: 8))
    cup.appendArc(withCenter: NSPoint(x: 16, y: 9), radius: 1, startAngle: 270, endAngle: 360)
    cup.line(to: NSPoint(x: 17, y: 17))
    cup.appendArc(withCenter: NSPoint(x: 13, y: 17), radius: 4, startAngle: 0, endAngle: 90)
    cup.line(to: NSPoint(x: 7, y: 21))
    cup.appendArc(withCenter: NSPoint(x: 7, y: 17), radius: 4, startAngle: 90, endAngle: 180)
    cup.line(to: NSPoint(x: 3, y: 9))
    cup.appendArc(withCenter: NSPoint(x: 4, y: 9), radius: 1, startAngle: 180, endAngle: 270)
    cup.line(to: NSPoint(x: 18, y: 8))
    cup.appendArc(withCenter: NSPoint(x: 18, y: 12), radius: 4, startAngle: 270, endAngle: 450)
    cup.line(to: NSPoint(x: 17, y: 16))
    cup.stroke()
    NSGraphicsContext.restoreGraphicsState()
    try bitmap.representation(using: .png, properties: [:])!.write(
        to: assets.appendingPathComponent("trayAwakeIconTemplate\(suffix).png"))
}
