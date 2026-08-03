import AppKit
import SwiftUI

enum BrandSymbol {
    private static let iconSize = NSSize(width: 18, height: 18)

    static func image(accessibilityDescription: String) -> NSImage {
        let image = NSImage(size: iconSize, flipped: false) { rect in
            drawIcon(in: rect)
            return true
        }
        image.isTemplate = true
        image.accessibilityDescription = accessibilityDescription
        return image
    }

    static var view: Image {
        Image(nsImage: image(accessibilityDescription: "ComputeHop"))
            .renderingMode(.template)
    }

    private static func drawIcon(in bounds: NSRect) {
        let scaleX = bounds.width / iconSize.width
        let scaleY = bounds.height / iconSize.height

        func point(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
            CGPoint(x: bounds.minX + (x * scaleX), y: bounds.minY + (y * scaleY))
        }

        func nodeRect(_ x: CGFloat, _ y: CGFloat, _ width: CGFloat, _ height: CGFloat) -> NSRect {
            NSRect(
                x: bounds.minX + (x * scaleX),
                y: bounds.minY + (y * scaleY),
                width: width * scaleX,
                height: height * scaleY
            )
        }

        NSColor.black.setStroke()
        NSColor.black.setFill()

        let top = nodeRect(7.0, 12.0, 4.0, 3.35)
        let left = nodeRect(2.25, 2.75, 4.0, 3.35)
        let right = nodeRect(11.75, 2.75, 4.0, 3.35)

        let links = NSBezierPath()
        links.lineWidth = 1.35 * min(scaleX, scaleY)
        links.lineCapStyle = .round
        links.lineJoinStyle = .round
        links.move(to: point(8.35, 12.0))
        links.curve(
            to: point(4.25, 6.1),
            controlPoint1: point(6.55, 10.35),
            controlPoint2: point(4.65, 8.5)
        )
        links.move(to: point(9.65, 12.0))
        links.curve(
            to: point(13.75, 6.1),
            controlPoint1: point(11.45, 10.35),
            controlPoint2: point(13.35, 8.5)
        )
        links.move(to: point(6.25, 4.45))
        links.curve(
            to: point(11.75, 4.45),
            controlPoint1: point(7.85, 3.55),
            controlPoint2: point(10.15, 3.55)
        )
        links.stroke()

        drawTile(top, scale: min(scaleX, scaleY))
        drawTile(left, scale: min(scaleX, scaleY))
        drawTile(right, scale: min(scaleX, scaleY))
    }

    private static func drawTile(_ rect: NSRect, scale: CGFloat) {
        NSBezierPath(
            roundedRect: rect,
            xRadius: 1.05 * scale,
            yRadius: 1.05 * scale
        ).fill()
    }
}
