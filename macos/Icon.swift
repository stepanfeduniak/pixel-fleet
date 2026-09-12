import AppKit
let image = NSImage(size: NSSize(width: 1024, height: 1024))
image.lockFocus()
NSColor(calibratedRed: 0.055, green: 0.09, blue: 0.16, alpha: 1).setFill()
NSBezierPath(roundedRect: NSRect(x: 0, y: 0, width: 1024, height: 1024), xRadius: 210, yRadius: 210).fill()
let ship = ["00100", "01110", "11111", "11011", "10001"]
for (cx, cy, size, alpha) in [(512.0, 590.0, 66.0, 1.0), (252.0, 330.0, 42.0, 0.65), (772.0, 330.0, 42.0, 0.65)] {
    NSColor(calibratedRed: 0.35, green: 0.84, blue: 0.95, alpha: alpha).setFill()
    for (y, row) in ship.enumerated() {
        for (x, pixel) in row.enumerated() where pixel == "1" {
            NSBezierPath(rect: NSRect(x: cx + Double(x - 2) * size - size / 2, y: cy + Double(2 - y) * size - size / 2, width: size - 4, height: size - 4)).fill()
        }
    }
}
image.unlockFocus()
let bitmap = NSBitmapImageRep(data: image.tiffRepresentation!)!
try bitmap.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
