// Owned-window discovery and PNG decoding only on the approved hosted runner.
import Foundation
import CoreGraphics
import ImageIO
import CryptoKit

guard ProcessInfo.processInfo.environment["GITHUB_ACTIONS"] == "true",
      ProcessInfo.processInfo.environment["RUNNER_ENVIRONMENT"] == "github-hosted",
      CommandLine.arguments.count == 3 else {
    fputs("requires a GitHub-hosted runner: window PID or pixels PNG\n", stderr)
    exit(2)
}
var result: [String: Any]
switch CommandLine.arguments[1] {
case "window":
    guard let pid = Int32(CommandLine.arguments[2]), pid > 1 else { exit(2) }
    guard CGPreflightScreenCaptureAccess() else {
        fputs("runner capture permission unavailable; no changes requested\n", stderr)
        exit(3)
    }
    let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
    let visibleOwned = windows.filter { info in
        (info[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == pid
    }
    let owned = visibleOwned.filter { info in
        guard (info[kCGWindowLayer as String] as? NSNumber)?.intValue == 0,
              let b = info[kCGWindowBounds as String] as? [String: Any],
              let w = b["Width"] as? NSNumber, let h = b["Height"] as? NSNumber else { return false }
        return w.doubleValue > 200 && h.doubleValue > 100
    }
    guard owned.count == 1, let id = owned[0][kCGWindowNumber as String] as? NSNumber else {
        // Diagnose only the launched process; never publish other windows.
        let diagnostic: [String: Any] = ["error": "expected one visible window owned by terminal PID",
            "pid": pid, "visible_owned_count": visibleOwned.count,
            "eligible_owned_count": owned.count, "visible_owned_windows": visibleOwned.prefix(8).map { info in
                ["id": info[kCGWindowNumber as String] ?? NSNull(),
                 "layer": info[kCGWindowLayer as String] ?? NSNull(),
                 "bounds": info[kCGWindowBounds as String] ?? [:]]
            }]
        let data = try JSONSerialization.data(withJSONObject: diagnostic, options: [.sortedKeys])
        FileHandle.standardError.write(data)
        FileHandle.standardError.write(Data("\n".utf8))
        exit(4)
    }
    result = ["pid": pid, "window_id": id, "bounds": owned[0][kCGWindowBounds as String] ?? [:]]
case "pixels":
    let url = URL(fileURLWithPath: CommandLine.arguments[2])
    guard let source = CGImageSourceCreateWithURL(url as CFURL, nil),
          let image = CGImageSourceCreateImageAtIndex(source, 0, nil),
          image.width > 0, image.width <= 8192, image.height > 0, image.height <= 8192 else {
        fputs("invalid or oversized capture image\n", stderr)
        exit(5)
    }
    var rgba = [UInt8](repeating: 0, count: image.width * image.height * 4)
    let ok = rgba.withUnsafeMutableBytes { bytes -> Bool in
        guard let context = CGContext(data: bytes.baseAddress, width: image.width, height: image.height,
                                      bitsPerComponent: 8, bytesPerRow: image.width * 4,
                                      space: CGColorSpaceCreateDeviceRGB(),
                                      bitmapInfo: CGBitmapInfo.byteOrder32Big.rawValue | CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
        context.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
        return true
    }
    guard ok else { exit(6) }
    let hash = SHA256.hash(data: Data(rgba)).map { String(format: "%02x", $0) }.joined()
    result = ["pixel_sha256": hash, "decoded_width": image.width, "decoded_height": image.height,
              "decoded_format": "RGBA8 premultiplied, device RGB"]
default:
    exit(2)
}
let data = try JSONSerialization.data(withJSONObject: result, options: [.sortedKeys])
FileHandle.standardOutput.write(data)
