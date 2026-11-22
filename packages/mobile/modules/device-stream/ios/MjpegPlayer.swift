import QuartzCore
import UIKit

/// Plays a simulator's multipart/x-mixed-replace MJPEG stream into an image
/// view. Frames are decoded in order off the main thread (skipping ahead if
/// decoding falls behind) and shown at an even pace through `Playout`; a frame
/// identical to the last (the simulator repeats a still screen) is skipped.
///
/// URLSession splits x-mixed-replace itself: each part arrives as its own
/// response carrying only the JPEG. A frame is shown as soon as its end marker
/// arrives rather than when the next part starts, which would show every frame
/// a frame late. Raw multipart bytes (no split) are parsed by Content-Length.
final class MjpegPlayer: NSObject, URLSessionDataDelegate {
  private let request: URLRequest
  private weak var imageView: UIImageView?
  private let onSize: (Int, Int) -> Void
  private let onFrame: () -> Void
  private let onEnded: () -> Void
  private var session: URLSession?
  private var buffer = Data()
  private var previous: Data?
  private let decodeQueue = DispatchQueue(label: "ao.device-stream.mjpeg-decode")
  private let lock = NSLock()
  /// Parts waiting to be decoded, with when each arrived.
  private var pending: [(data: Data, arrival: CFTimeInterval)] = []
  /// Main thread only.
  private let playout = Playout()
  private var decoding = false
  private var stopped = false
  private var shownFirst = false
  private var pixelSize = (0, 0)
  /// URLSession is handing over one part per response (the usual case).
  private var splitParts = false
  /// When the current part's first bytes came, for `takeStats`.
  private var partStart: CFTimeInterval?
  private let statsLock = NSLock()
  private var statsBytes = 0
  private var statsFrames = 0
  private var statsBusy: CFTimeInterval = 0
  private var statsSince = CACurrentMediaTime()

  init(url: URL, headers: [String: String], imageView: UIImageView, onSize: @escaping (Int, Int) -> Void, onFrame: @escaping () -> Void, onEnded: @escaping () -> Void) {
    var request = URLRequest(url: url)
    request.cachePolicy = .reloadIgnoringLocalCacheData
    for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
    self.request = request
    self.imageView = imageView
    self.onSize = onSize
    self.onFrame = onFrame
    self.onEnded = onEnded
  }

  func start() {
    let queue = OperationQueue()
    queue.maxConcurrentOperationCount = 1
    let config = URLSessionConfiguration.default
    config.timeoutIntervalForRequest = 60
    config.timeoutIntervalForResource = .greatestFiniteMagnitude
    let session = URLSession(configuration: config, delegate: self, delegateQueue: queue)
    self.session = session
    session.dataTask(with: request).resume()
  }

  func stop() {
    stopped = true
    session?.invalidateAndCancel()
    session = nil
    let playout = self.playout
    DispatchQueue.main.async { playout.stop() }
  }

  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse, completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
    if let mime = response.mimeType, mime.hasPrefix("image/") {
      // The next part has begun: whatever the last left unfinished is dropped.
      splitParts = true
      partStart = nil
      buffer.removeAll(keepingCapacity: true)
      completionHandler(.allow)
      return
    }
    let ok = (response as? HTTPURLResponse).map { (200..<300).contains($0.statusCode) } ?? false
    completionHandler(ok ? .allow : .cancel)
  }

  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
    if stopped { return }
    let now = CACurrentMediaTime()
    statsLock.lock()
    statsBytes += data.count
    statsLock.unlock()
    if partStart == nil { partStart = now }
    buffer.append(data)
    if splitParts {
      // A JPEG ends with FF D9: the frame is whole, show it now.
      let count = buffer.count
      if count >= 2, buffer[buffer.startIndex + count - 2] == 0xFF, buffer[buffer.startIndex + count - 1] == 0xD9 {
        let part = buffer
        buffer = Data()
        if let start = partStart {
          statsLock.lock()
          statsBusy += now - start
          statsFrames += 1
          statsLock.unlock()
        }
        partStart = nil
        if part != previous {
          previous = part
          publish(part)
        }
      }
      return
    }
    parse()
  }

  func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
    if !stopped { onEnded() }
  }

  /// How the link has carried the stream since the last call: kilobytes per
  /// second, parts, and `busy`, the share of the time spent receiving a part.
  /// The hub sends the newest frame as soon as the last one is through, so a
  /// link that cannot carry the stream is busy nearly all the time.
  func takeStats() -> [String: Any] {
    statsLock.lock()
    defer { statsLock.unlock() }
    let now = CACurrentMediaTime()
    let span = max(now - statsSince, 0.001)
    let stats: [String: Any] = ["kbps": Double(statsBytes) * 8 / 1000 / span, "frames": statsFrames, "busy": min(1, statsBusy / span)]
    statsBytes = 0
    statsFrames = 0
    statsBusy = 0
    statsSince = now
    return stats
  }

  private static let blankLine = Data([13, 10, 13, 10])

  private func parse() {
    while true {
      guard let headerEnd = buffer.range(of: MjpegPlayer.blankLine) else {
        if buffer.count > 64 * 1024 { buffer.removeAll() }
        return
      }
      let header = String(decoding: buffer[buffer.startIndex..<headerEnd.lowerBound], as: UTF8.self)
      let bodyStart = headerEnd.upperBound
      guard let length = MjpegPlayer.contentLength(header) else {
        buffer = Data(buffer[bodyStart...])
        continue
      }
      let bodyEnd = bodyStart + length
      if buffer.endIndex < bodyEnd { return }
      let part = Data(buffer[bodyStart..<bodyEnd])
      buffer = Data(buffer[bodyEnd...])
      if part == previous { continue }
      previous = part
      publish(part)
    }
  }

  private static func contentLength(_ header: String) -> Int? {
    for line in header.split(whereSeparator: { $0 == "\r" || $0 == "\n" }) {
      let pieces = line.split(separator: ":", maxSplits: 1)
      if pieces.count == 2, pieces[0].trimmingCharacters(in: .whitespaces).lowercased() == "content-length" {
        return Int(pieces[1].trimmingCharacters(in: .whitespaces))
      }
    }
    return nil
  }

  private func publish(_ part: Data) {
    lock.lock()
    let now = CACurrentMediaTime()
    pending.append((part, now))
    // A burst after a Wi-Fi pause is kept whole for Playout to spread out;
    // only frames already too late to be shown are skipped.
    while pending.count > 1, now - pending[0].arrival > Playout.maxDelay { pending.removeFirst() }
    let start = !decoding
    decoding = true
    lock.unlock()
    if start { decodeQueue.async { [weak self] in self?.drain() } }
  }

  private func drain() {
    while true {
      lock.lock()
      guard !pending.isEmpty, !stopped else {
        decoding = false
        lock.unlock()
        return
      }
      let (data, arrival) = pending.removeFirst()
      lock.unlock()
      guard let raw = UIImage(data: data) else { continue }
      let image = raw.preparingForDisplay() ?? raw
      let size = (image.cgImage?.width ?? Int(image.size.width), image.cgImage?.height ?? Int(image.size.height))
      DispatchQueue.main.async { [weak self] in
        guard let self, !self.stopped else { return }
        self.playout.push(arrival: arrival, interval: nil) { [weak self] in
          self?.show(image, size: size)
        }
      }
    }
  }

  /// Main thread.
  private func show(_ image: UIImage, size: (Int, Int)) {
    guard !stopped else { return }
    imageView?.image = image
    if size != pixelSize {
      pixelSize = size
      onSize(size.0, size.1)
    }
    if !shownFirst {
      shownFirst = true
      onFrame()
    }
  }
}
