import AVFoundation
import CoreMedia
import QuartzCore
import UIKit

/// Plays a simulator's H.264 stream (the hub's stream.avcc) into a sample-buffer
/// layer, which decodes in hardware. It carries a scrolling simulator in ~5
/// Mbit/s where MJPEG needs ~112, so a phone keeps full quality on a link MJPEG
/// would swamp.
///
/// The stream is length-prefixed packets: a 4-byte length (counting the type
/// byte), a type, then the payload. Type 4 is a JPEG of the current screen,
/// sent first and shown under the video until it draws; 1 is the decoder
/// setup (an avcC record); 2 and 3 are key and delta frames as length-prefixed
/// NAL units, which is what the layer takes. Frames are paced like MJPEG's
/// (see `Playout`). There is no way to ask this stream for a key frame, so a
/// decoder that loses its place reconnects: every connection starts with one.
final class AvccPlayer: NSObject, URLSessionDataDelegate {
  private let request: URLRequest
  private weak var layer: AVSampleBufferDisplayLayer?
  private weak var placeholder: UIImageView?
  private let onSize: (Int, Int) -> Void
  private let onFrame: () -> Void
  private let onEnded: () -> Void
  private var session: URLSession?
  private var buffer = Data()
  private var format: CMVideoFormatDescription?
  private var waitingForKey = true
  private var stopped = false
  private var shownFirst = false
  private var endedOnce = false
  /// Main thread only.
  private let playout = Playout()
  private let decodeQueue = DispatchQueue(label: "ao.device-stream.avcc-placeholder")

  private let statsLock = NSLock()
  private var statsBytes = 0
  private var statsFrames = 0
  private var statsBusy: CFTimeInterval = 0
  private var statsSince = CACurrentMediaTime()
  /// When the packet being received began to arrive.
  private var packetStart: CFTimeInterval?

  init(url: URL, headers: [String: String], layer: AVSampleBufferDisplayLayer, placeholder: UIImageView, onSize: @escaping (Int, Int) -> Void, onFrame: @escaping () -> Void, onEnded: @escaping () -> Void) {
    var request = URLRequest(url: url)
    request.cachePolicy = .reloadIgnoringLocalCacheData
    for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
    self.request = request
    self.layer = layer
    self.placeholder = placeholder
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
    let layer = self.layer
    let playout = self.playout
    DispatchQueue.main.async {
      playout.stop()
      layer?.flushAndRemoveImage()
    }
  }

  /// How the link has carried the stream since the last call: kilobytes per
  /// second, frames, and `busy`, the share of the time spent receiving a
  /// packet. The hub sends the next frame as soon as the last is through, so a
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

  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse, completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
    let ok = (response as? HTTPURLResponse).map { (200..<300).contains($0.statusCode) } ?? false
    completionHandler(ok ? .allow : .cancel)
  }

  func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
    if stopped { return }
    let now = CACurrentMediaTime()
    statsLock.lock()
    statsBytes += data.count
    statsLock.unlock()
    if packetStart == nil { packetStart = now }
    buffer.append(data)
    while buffer.count >= 5 {
      let base = buffer.startIndex
      let length = buffer[base..<base + 4].reduce(0) { $0 << 8 | Int($1) }
      guard length >= 1, length <= 16 << 20 else {
        end()
        return
      }
      if buffer.count < 4 + length { break }
      let type = buffer[base + 4]
      let payload = Data(buffer[(base + 5)..<(base + 4 + length)])
      buffer = Data(buffer[(base + 4 + length)...])
      if let start = packetStart {
        statsLock.lock()
        statsBusy += now - start
        statsLock.unlock()
      }
      packetStart = buffer.isEmpty ? nil : now
      handle(type: type, payload: payload, arrival: now)
    }
  }

  func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
    if !stopped { end() }
  }

  private func end() {
    if endedOnce { return }
    endedOnce = true
    onEnded()
  }

  private func handle(type: UInt8, payload: Data, arrival: CFTimeInterval) {
    switch type {
    case 4:
      showPlaceholder(payload)
    case 1:
      guard let (sps, pps) = AvccPlayer.parameterSets(payload), let next = H264Player.makeFormat(sps: sps, pps: pps) else { return }
      format = next
      waitingForKey = true
      let dims = CMVideoFormatDescriptionGetDimensions(next)
      onSize(Int(dims.width), Int(dims.height))
    case 2, 3:
      guard let format else { return }
      let isKey = type == 2
      if waitingForKey && !isKey { return }
      guard let sample = H264Player.makeSample(payload, format: format) else { return }
      if isKey { waitingForKey = false }
      statsLock.lock()
      statsFrames += 1
      statsLock.unlock()
      DispatchQueue.main.async { [weak self] in
        guard let self, !self.stopped else { return }
        self.playout.push(arrival: arrival, interval: nil) { [weak self] in
          self?.show(sample, isKey: isKey)
        }
      }
    default:
      break
    }
  }

  /// Main thread.
  private func show(_ sample: CMSampleBuffer, isKey: Bool) {
    guard let layer, !stopped else { return }
    // Lost its place (a failed decode, or frames it could not take): only a
    // new connection brings a key frame.
    if layer.status == .failed || (!isKey && !layer.isReadyForMoreMediaData) {
      end()
      return
    }
    layer.enqueue(sample)
    if !shownFirst {
      shownFirst = true
      onFrame()
    }
  }

  private func showPlaceholder(_ jpeg: Data) {
    decodeQueue.async { [weak self] in
      guard let image = UIImage(data: jpeg)?.preparingForDisplay() else { return }
      DispatchQueue.main.async {
        guard let self, !self.stopped else { return }
        self.placeholder?.image = image
        if !self.shownFirst {
          self.shownFirst = true
          self.onFrame()
        }
      }
    }
  }

  /// The SPS and PPS in an avcC record (ISO/IEC 14496-15). Frames use 4-byte
  /// lengths, which is what H264Player's samples are built with.
  static func parameterSets(_ record: Data) -> ([UInt8], [UInt8])? {
    let b = [UInt8](record)
    guard b.count > 7, b[0] == 1, (b[4] & 0x03) + 1 == 4 else { return nil }
    var i = 5
    let spsCount = Int(b[i] & 0x1f)
    i += 1
    var sps: [UInt8]?
    for _ in 0..<spsCount {
      guard i + 2 <= b.count else { return nil }
      let n = Int(b[i]) << 8 | Int(b[i + 1])
      i += 2
      guard i + n <= b.count else { return nil }
      if sps == nil { sps = Array(b[i..<i + n]) }
      i += n
    }
    guard i < b.count else { return nil }
    let ppsCount = Int(b[i])
    i += 1
    var pps: [UInt8]?
    for _ in 0..<ppsCount {
      guard i + 2 <= b.count else { return nil }
      let n = Int(b[i]) << 8 | Int(b[i + 1])
      i += 2
      guard i + n <= b.count else { return nil }
      if pps == nil { pps = Array(b[i..<i + n]) }
      i += n
    }
    guard let sps, let pps else { return nil }
    return (sps, pps)
  }
}
