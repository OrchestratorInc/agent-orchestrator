import AVFoundation
import CoreMedia

/// Plays an emulator's H.264 WebSocket stream (one Annex-B access unit per
/// message, behind the hub's SEMU header) into a sample-buffer layer, which
/// decodes in hardware. After a gap or a layer error it waits for the next key
/// frame, asking for one, rather than showing a smeared picture. Frames are
/// paced by the capture time in that header, so a burst after a Wi-Fi pause
/// plays out at the pace it was recorded (see `Playout`).
final class H264Player: NSObject, URLSessionWebSocketDelegate {
  private let request: URLRequest
  private weak var layer: AVSampleBufferDisplayLayer?
  private let onSize: (Int, Int) -> Void
  private let onFrame: () -> Void
  private let onEnded: () -> Void
  private var session: URLSession?
  private var task: URLSessionWebSocketTask?
  private var format: CMVideoFormatDescription?
  private var parameterSets = Data()
  private var waitingForKey = true
  private var lastKeyRequest = Date.distantPast
  private var stopped = false
  private var shownFirst = false
  /// The previous frame's capture time, microseconds.
  private var lastCapture: UInt64 = 0
  /// Main thread only.
  private let playout = Playout()
  private let statsLock = NSLock()
  private var statsBytes = 0
  private var statsFrames = 0
  private var statsSince = CACurrentMediaTime()
  /// Arrival minus capture (ms) of frames sent in motion this window, the
  /// lowest of recent windows, and the latest of any frame.
  private var offsetSum: Double = 0
  private var offsetCount = 0
  private var offsetMin = Double.greatestFiniteMagnitude
  private var offsetMax = -Double.greatestFiniteMagnitude
  private var recentMins: [Double] = []
  private var lastArrival: CFTimeInterval = 0

  init(url: URL, headers: [String: String], layer: AVSampleBufferDisplayLayer, onSize: @escaping (Int, Int) -> Void, onFrame: @escaping () -> Void, onEnded: @escaping () -> Void) {
    var request = URLRequest(url: url)
    for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
    self.request = request
    self.layer = layer
    self.onSize = onSize
    self.onFrame = onFrame
    self.onEnded = onEnded
  }

  func start() {
    let queue = OperationQueue()
    queue.maxConcurrentOperationCount = 1
    let session = URLSession(configuration: .default, delegate: self, delegateQueue: queue)
    self.session = session
    let task = session.webSocketTask(with: request)
    task.maximumMessageSize = 16 << 20
    self.task = task
    task.resume()
    receive()
  }

  func stop() {
    stopped = true
    task?.cancel(with: .goingAway, reason: nil)
    task = nil
    session?.invalidateAndCancel()
    session = nil
    let layer = self.layer
    let playout = self.playout
    DispatchQueue.main.async {
      playout.stop()
      layer?.flushAndRemoveImage()
    }
  }

  private func receive() {
    task?.receive { [weak self] result in
      guard let self, !self.stopped else { return }
      switch result {
      case .success(let message):
        if case .data(let data) = message { self.handle(data) }
        self.receive()
      case .failure:
        self.onEnded()
      }
    }
  }

  func urlSession(_ session: URLSession, webSocketTask: URLSessionWebSocketTask, didCloseWith closeCode: URLSessionWebSocketTask.CloseCode, reason: Data?) {
    if !stopped { onEnded() }
  }

  /// How the link has carried the stream since the last call: kilobytes per
  /// second, frames, `lagMs` (how much later than usual frames in motion
  /// arrived after capture) and `maxLagMs` (the latest frame of all). A link
  /// that cannot carry the bitrate queues frames, so the lag keeps growing.
  func takeStats() -> [String: Any] {
    statsLock.lock()
    defer { statsLock.unlock() }
    let now = CACurrentMediaTime()
    let span = max(now - statsSince, 0.001)
    var stats: [String: Any] = ["kbps": Double(statsBytes) * 8 / 1000 / span, "frames": statsFrames]
    if offsetCount > 0 {
      recentMins.append(offsetMin)
      if recentMins.count > 5 { recentMins.removeFirst(recentMins.count - 5) }
    }
    if let floor = recentMins.min() {
      if offsetCount >= 3 { stats["lagMs"] = max(0, offsetSum / Double(offsetCount) - floor) }
      if statsFrames > 0 { stats["maxLagMs"] = max(0, offsetMax - floor) }
    }
    statsBytes = 0
    statsFrames = 0
    offsetSum = 0
    offsetCount = 0
    offsetMin = .greatestFiniteMagnitude
    offsetMax = -.greatestFiniteMagnitude
    statsSince = now
    return stats
  }

  private func requestKeyFrame() {
    let now = Date()
    if now.timeIntervalSince(lastKeyRequest) < 1 { return }
    lastKeyRequest = now
    task?.send(.string("{\"type\":\"reset-video\"}")) { _ in }
  }

  private func handle(_ message: Data) {
    let arrival = CACurrentMediaTime()
    statsLock.lock()
    statsBytes += message.count
    statsLock.unlock()
    var bytes = [UInt8](message)
    var interval: CFTimeInterval?
    // The hub's SEMU header: v1 is 16 bytes, v2 24; bytes 8-15 are the capture
    // time in microseconds, big-endian.
    if bytes.count > 24, bytes[0] == 0x53, bytes[1] == 0x45, bytes[2] == 0x4d, bytes[3] == 0x55 {
      let capture = bytes[8..<16].reduce(UInt64(0)) { $0 << 8 | UInt64($1) }
      let offset = arrival * 1000 - Double(capture) / 1000
      statsLock.lock()
      // A still screen is refreshed every half second with a frame whose
      // capture time stays put: only frames in motion set the usual lateness.
      if arrival - lastArrival < 0.3 {
        offsetSum += offset
        offsetCount += 1
        offsetMin = min(offsetMin, offset)
      }
      offsetMax = max(offsetMax, offset)
      lastArrival = arrival
      statsFrames += 1
      statsLock.unlock()
      if lastCapture > 0, capture > lastCapture, capture - lastCapture < 5_000_000 {
        interval = CFTimeInterval(capture - lastCapture) / 1_000_000
      }
      lastCapture = capture
      bytes.removeFirst(bytes[4] == 1 ? 16 : 24)
    }
    let nals = H264Player.split(bytes)
    let sps = nals.first { $0.type == 7 }
    let pps = nals.first { $0.type == 8 }
    let isKey = nals.contains { $0.type == 5 }
    if let sps, let pps {
      let spsBytes = Array(bytes[sps.range]), ppsBytes = Array(bytes[pps.range])
      let sets = Data(spsBytes + ppsBytes)
      if format == nil || sets != parameterSets, let next = H264Player.makeFormat(sps: spsBytes, pps: ppsBytes) {
        format = next
        parameterSets = sets
        waitingForKey = true
        let dims = CMVideoFormatDescriptionGetDimensions(next)
        onSize(Int(dims.width), Int(dims.height))
        let layer = self.layer
        let playout = self.playout
        DispatchQueue.main.async {
          playout.reset()
          layer?.flush()
        }
      }
    }
    guard let format else {
      requestKeyFrame()
      return
    }
    if waitingForKey && !isKey {
      requestKeyFrame()
      return
    }
    // Length-prefixed NAL units, without the parameter sets and delimiters.
    var avcc = Data()
    for nal in nals where nal.type != 7 && nal.type != 8 && nal.type != 9 {
      var length = UInt32(nal.range.count).bigEndian
      withUnsafeBytes(of: &length) { avcc.append(contentsOf: $0) }
      avcc.append(contentsOf: bytes[nal.range])
    }
    guard !avcc.isEmpty, let sample = H264Player.makeSample(avcc, format: format) else { return }
    if isKey { waitingForKey = false }
    let first = !shownFirst
    shownFirst = true
    DispatchQueue.main.async { [weak self] in
      guard let self, !self.stopped else { return }
      self.playout.push(arrival: arrival, interval: interval) { [weak self] in
        self?.show(sample, isKey: isKey, first: first)
      }
    }
  }

  /// Hands a sample to the layer when its turn comes. Main thread.
  private func show(_ sample: CMSampleBuffer, isKey: Bool, first: Bool) {
    guard let layer, !stopped else { return }
    if layer.status == .failed {
      layer.flush()
      waitingForKey = true
      requestKeyFrame()
      return
    }
    // Behind: drop to the next key frame instead of queueing late frames.
    if !isKey && !layer.isReadyForMoreMediaData {
      waitingForKey = true
      requestKeyFrame()
      return
    }
    layer.enqueue(sample)
    if first { onFrame() }
  }

  private struct Nal {
    let type: UInt8
    let range: Range<Int>
  }

  /// Splits an Annex-B access unit (3- or 4-byte start codes) into NAL units.
  private static func split(_ bytes: [UInt8]) -> [Nal] {
    var starts: [Int] = []
    var i = 0
    while i + 2 < bytes.count {
      if bytes[i] == 0, bytes[i + 1] == 0, bytes[i + 2] == 1 {
        starts.append(i + 3)
        i += 3
      } else {
        i += 1
      }
    }
    var nals: [Nal] = []
    for (index, start) in starts.enumerated() {
      var end = index + 1 < starts.count ? starts[index + 1] - 3 : bytes.count
      while end > start && bytes[end - 1] == 0 { end -= 1 }
      if end > start { nals.append(Nal(type: bytes[start] & 0x1f, range: start..<end)) }
    }
    return nals
  }

  static func makeFormat(sps: [UInt8], pps: [UInt8]) -> CMVideoFormatDescription? {
    var format: CMVideoFormatDescription?
    let status = sps.withUnsafeBufferPointer { spsBuffer in
      pps.withUnsafeBufferPointer { ppsBuffer -> OSStatus in
        let pointers: [UnsafePointer<UInt8>] = [spsBuffer.baseAddress!, ppsBuffer.baseAddress!]
        let sizes = [sps.count, pps.count]
        return CMVideoFormatDescriptionCreateFromH264ParameterSets(
          allocator: kCFAllocatorDefault, parameterSetCount: 2, parameterSetPointers: pointers,
          parameterSetSizes: sizes, nalUnitHeaderLength: 4, formatDescriptionOut: &format)
      }
    }
    return status == noErr ? format : nil
  }

  static func makeSample(_ avcc: Data, format: CMVideoFormatDescription) -> CMSampleBuffer? {
    var block: CMBlockBuffer?
    guard CMBlockBufferCreateWithMemoryBlock(
      allocator: kCFAllocatorDefault, memoryBlock: nil, blockLength: avcc.count, blockAllocator: kCFAllocatorDefault,
      customBlockSource: nil, offsetToData: 0, dataLength: avcc.count, flags: 0, blockBufferOut: &block) == kCMBlockBufferNoErr,
      let block else { return nil }
    let copied = avcc.withUnsafeBytes { raw in
      CMBlockBufferReplaceDataBytes(with: raw.baseAddress!, blockBuffer: block, offsetIntoDestination: 0, dataLength: avcc.count)
    }
    guard copied == kCMBlockBufferNoErr else { return nil }
    var sample: CMSampleBuffer?
    var size = avcc.count
    guard CMSampleBufferCreateReady(
      allocator: kCFAllocatorDefault, dataBuffer: block, formatDescription: format, sampleCount: 1,
      sampleTimingEntryCount: 0, sampleTimingArray: nil, sampleSizeEntryCount: 1, sampleSizeArray: &size,
      sampleBufferOut: &sample) == noErr, let sample else { return nil }
    if let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: true), CFArrayGetCount(attachments) > 0 {
      let dict = unsafeBitCast(CFArrayGetValueAtIndex(attachments, 0), to: CFMutableDictionary.self)
      CFDictionarySetValue(dict, Unmanaged.passUnretained(kCMSampleAttachmentKey_DisplayImmediately).toOpaque(), Unmanaged.passUnretained(kCFBooleanTrue).toOpaque())
    }
    return sample
  }
}
