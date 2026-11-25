import AVFoundation
import ExpoModulesCore
import UIKit

/// The phone's live view of a simulator or emulator, as on Android: the
/// emulator's H.264 over a WebSocket into a sample-buffer layer (hardware
/// decode), or the simulator's MJPEG parsed and drawn newest frame first. No
/// JavaScript sits in the video path; touches stay in JS on their own socket.
final class DeviceStreamView: ExpoView {
  let onState = EventDispatcher()
  let onSize = EventDispatcher()
  let onStats = EventDispatcher()
  private var statsTimer: Timer?

  private var imageView = UIImageView()
  private var videoLayer = AVSampleBufferDisplayLayer()
  private var mjpeg: MjpegPlayer?
  private var h264: H264Player?
  private var avcc: AvccPlayer?
  private var connectPending = false
  /// Whether this connection has shown a frame (see startStats).
  private var gotFrame = false
  /// Connections lost in a row without a frame; each waits longer before retrying.
  private var failures = 0
  /// The pending retry after a loss; a newer connection or target cancels it,
  /// or it would tear down a healthy stream when it fires.
  private var retry: DispatchWorkItem?
  /// The live connection's number. stop() moves past it, so callbacks from an
  /// older player cannot end or relabel the current one.
  private var generation = 0

  /// "h264" (the emulator's WebSocket video), "avcc" (the simulator's H.264
  /// over HTTP) or "mjpeg" (the simulator's multipart stream).
  var mode = "h264" {
    didSet { if oldValue != mode { retarget() } }
  }
  var url: String? {
    didSet { if oldValue != url { retarget() } }
  }
  var headers: [String: String] = [:] {
    didSet { if oldValue != headers { retarget() } }
  }

  /// A new target starts the retry delay over.
  private func retarget() {
    failures = 0
    retry?.cancel()
    retry = nil
    scheduleConnect()
  }

  required init(appContext: AppContext? = nil) {
    super.init(appContext: appContext)
    clipsToBounds = true
    backgroundColor = .black
    imageView.contentMode = .scaleToFill
    addSubview(imageView)
    videoLayer.videoGravity = .resize
    layer.addSublayer(videoLayer)
  }

  override func layoutSubviews() {
    super.layoutSubviews()
    imageView.frame = bounds
    CATransaction.begin()
    CATransaction.setDisableActions(true)
    videoLayer.frame = bounds
    CATransaction.commit()
  }

  override func didMoveToWindow() {
    super.didMoveToWindow()
    if window == nil { stop() } else { scheduleConnect() }
  }

  /// Mode, url and headers arrive as separate props: connect once all have landed.
  private func scheduleConnect() {
    if connectPending { return }
    connectPending = true
    DispatchQueue.main.async { [weak self] in
      guard let self else { return }
      self.connectPending = false
      self.connect()
    }
  }

  /// [quiet]: a reconnect of a stream that went quiet on a still screen; the
  /// last picture stays up meanwhile, without a "connecting" state.
  private func connect(quiet: Bool = false) {
    let previous = quiet ? imageView.image : nil
    stop()
    gotFrame = false
    guard window != nil, let raw = url, let target = URL(string: raw) else { return }
    if !quiet { emit(state: "connecting") }
    let gen = generation
    let size: (Int, Int) -> Void = { [weak self] w, h in
      DispatchQueue.main.async {
        guard let self, self.generation == gen else { return }
        self.onSize(["width": w, "height": h])
      }
    }
    let streaming: () -> Void = { [weak self] in
      DispatchQueue.main.async {
        guard let self, self.generation == gen else { return }
        self.gotFrame = true
        self.failures = 0
        self.emit(state: "streaming")
      }
    }
    let lost: () -> Void = { [weak self] in
      DispatchQueue.main.async {
        guard let self, self.generation == gen else { return }
        self.lost()
      }
    }
    if mode == "mjpeg" {
      videoLayer.isHidden = true
      // A fresh image view per connection: a view React Native reused kept its
      // old one, which took new frames but drew nothing.
      imageView.removeFromSuperview()
      imageView = UIImageView(frame: bounds)
      imageView.contentMode = .scaleToFill
      imageView.image = previous
      insertSubview(imageView, at: 0)
      imageView.isHidden = false
      let player = MjpegPlayer(url: target, headers: headers, imageView: imageView, onSize: size, onFrame: streaming, onEnded: lost)
      mjpeg = player
      player.start()
      startStats { [weak player] in player?.takeStats() }
    } else if mode == "avcc" {
      // The opening JPEG shows in the image view until the video, drawn above
      // it, has its first frame. Both fresh per connection, as below.
      imageView.removeFromSuperview()
      imageView = UIImageView(frame: bounds)
      imageView.contentMode = .scaleToFill
      insertSubview(imageView, at: 0)
      imageView.isHidden = false
      videoLayer.removeFromSuperlayer()
      videoLayer = AVSampleBufferDisplayLayer()
      videoLayer.videoGravity = .resize
      CATransaction.begin()
      CATransaction.setDisableActions(true)
      videoLayer.frame = bounds
      CATransaction.commit()
      layer.addSublayer(videoLayer)
      videoLayer.isHidden = false
      let player = AvccPlayer(url: target, headers: headers, layer: videoLayer, placeholder: imageView, onSize: size, onFrame: streaming, onEnded: lost)
      avcc = player
      player.start()
      startStats { [weak player] in player?.takeStats() }
    } else {
      imageView.isHidden = true
      // A fresh layer per connection, as for the image view above.
      videoLayer.removeFromSuperlayer()
      videoLayer = AVSampleBufferDisplayLayer()
      videoLayer.videoGravity = .resize
      CATransaction.begin()
      CATransaction.setDisableActions(true)
      videoLayer.frame = bounds
      CATransaction.commit()
      layer.addSublayer(videoLayer)
      videoLayer.isHidden = false
      let player = H264Player(url: target, headers: headers, layer: videoLayer, onSize: size, onFrame: streaming, onEnded: lost)
      h264 = player
      player.start()
      startStats { [weak player] in player?.takeStats() }
    }
  }

  /// Reports how the link is carrying the stream every two seconds, so the app
  /// can pick the stream quality the connection can take.
  ///
  /// A link silent for three reports (6 s) before its first frame is dead, for
  /// instance one opened while the device was still booting: it is dropped and
  /// opened again rather than waiting out the request timeout on a black
  /// screen. The emulator's socket also sends something every half second for
  /// a still screen, so the same holds after it streams. The simulator's
  /// streams send nothing while the screen stands still: once they have shown
  /// a frame, only a minute of silence counts, and the reconnect is quiet.
  private func startStats(_ take: @escaping () -> [String: Any]?) {
    statsTimer?.invalidate()
    _ = take() // start the first window now
    var silent = 0
    statsTimer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in
      guard let self, let stats = take() else { return }
      silent = (stats["kbps"] as? Double ?? 0) > 0 ? 0 : silent + 1
      let still = self.mode != "h264" && self.gotFrame
      if silent >= (still ? 30 : 3) {
        if still { self.connect(quiet: true) } else { self.lost() }
        return
      }
      var event = stats
      event["mode"] = self.mode
      self.onStats(event)
    }
  }

  private func stop() {
    generation += 1
    retry?.cancel()
    retry = nil
    statsTimer?.invalidate()
    statsTimer = nil
    mjpeg?.stop()
    mjpeg = nil
    h264?.stop()
    h264 = nil
    avcc?.stop()
    avcc = nil
  }

  /// Retries 1.5 s after a loss, doubling up to 30 s while connections keep
  /// failing before a frame (a daemon without the route, a refused token).
  private func lost() {
    guard mjpeg != nil || h264 != nil || avcc != nil else { return }
    emit(state: "closed")
    stop()
    let delay = min(1.5 * pow(2, Double(min(failures, 5))), 30)
    failures += 1
    let work = DispatchWorkItem { [weak self] in
      self?.retry = nil
      self?.scheduleConnect()
    }
    retry = work
    DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
  }

  private func emit(state: String) {
    onState(["state": state])
  }
}
