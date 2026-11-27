import QuartzCore

/// Shows frames at the pace they were captured when the network hands them
/// over in bursts. An iPhone's Wi-Fi leaves the channel for ~100 ms twice a
/// second while AWDL (AirDrop, Continuity) is active, so frames stop and then
/// land all at once: shown on arrival that is a freeze and a jump. Each frame
/// is instead shown one capture interval after the one before it (never before
/// it arrives), so the frames that queue up behind a gap keep the picture
/// moving through the next one.
///
/// The delay this adds only builds when frames arrive late: frames play out a
/// little faster than they were captured, so on a steady link it drains back to
/// nothing, and it never exceeds `maxDelay`. Main thread only.
final class Playout {
  /// The most a frame waits behind its arrival.
  static let maxDelay: CFTimeInterval = 0.15
  /// Frames play out this much faster than captured so the delay drains.
  private static let drain = 0.98

  private struct Frame {
    let due: CFTimeInterval
    let show: () -> Void
  }

  private var queue: [Frame] = []
  private var lastDue: CFTimeInterval = 0
  private var lastArrival: CFTimeInterval = 0
  private var gaps: [CFTimeInterval] = []
  private var link: CADisplayLink?

  /// Queues a frame that arrived at `arrival` (`CACurrentMediaTime`).
  /// `interval` is how long after the previous frame it was captured, when the
  /// stream says; otherwise the usual gap between arrivals stands in for it.
  func push(arrival: CFTimeInterval, interval: CFTimeInterval?, show: @escaping () -> Void) {
    let spacing = interval.map { max(0, $0) } ?? typicalGap(arrival)
    var due = max(arrival, lastDue + spacing * Playout.drain)
    if due - arrival > Playout.maxDelay { due = arrival + Playout.maxDelay }
    lastDue = due
    if queue.isEmpty && due <= CACurrentMediaTime() {
      show()
      return
    }
    queue.append(Frame(due: due, show: show))
    startLink()
  }

  /// Drops queued frames (a new picture format, a reconnect).
  func reset() {
    queue.removeAll()
    lastDue = 0
    lastArrival = 0
    gaps.removeAll()
    link?.isPaused = true
  }

  func stop() {
    reset()
    link?.invalidate()
    link = nil
  }

  /// The median gap between recent arrivals, leaving out frames that landed
  /// together and the pauses between them.
  private func typicalGap(_ arrival: CFTimeInterval) -> CFTimeInterval {
    let gap = arrival - lastArrival
    lastArrival = arrival
    if gap > 0.005 && gap < 0.2 {
      gaps.append(gap)
      if gaps.count > 30 { gaps.removeFirst(gaps.count - 30) }
    }
    if gaps.isEmpty { return 1.0 / 60 }
    return gaps.sorted()[gaps.count / 2]
  }

  private func startLink() {
    if let link {
      link.isPaused = false
      return
    }
    let proxy = Tick(self)
    let link = CADisplayLink(target: proxy, selector: #selector(Tick.fire(_:)))
    link.preferredFrameRateRange = CAFrameRateRange(minimum: 30, maximum: 120, preferred: 120)
    link.add(to: .main, forMode: .common)
    self.link = link
  }

  fileprivate func tick(_ link: CADisplayLink) {
    // Everything due by the time this frame reaches the screen, in order:
    // H.264 needs every sample decoded even when only the last is seen.
    let by = link.targetTimestamp
    var shown = 0
    while shown < queue.count && queue[shown].due <= by { shown += 1 }
    if shown > 0 {
      let due = queue[0..<shown]
      queue.removeFirst(shown)
      for frame in due { frame.show() }
    }
    if queue.isEmpty { link.isPaused = true }
  }
}

/// Keeps the display link from retaining its playout.
private final class Tick: NSObject {
  private weak var owner: Playout?
  init(_ owner: Playout) { self.owner = owner }
  @objc func fire(_ link: CADisplayLink) { owner?.tick(link) }
}
