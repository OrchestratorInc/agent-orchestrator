package ao.mobile.devicestream

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.ViewGroup
import expo.modules.kotlin.AppContext
import expo.modules.kotlin.viewevent.EventDispatcher
import expo.modules.kotlin.views.ExpoView
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import java.util.concurrent.TimeUnit

/**
 * The phone's live view of a simulator or emulator: a WebSocket to the daemon's
 * device stream route, decoded by the hardware H.264 decoder onto a Surface. No
 * JavaScript sits in the video path. Touches stay in JS on a separate socket.
 */
class DeviceStreamView(context: Context, appContext: AppContext) : ExpoView(context, appContext), SurfaceHolder.Callback {
  override val shouldUseAndroidLayout = true

  private val onState by EventDispatcher<Map<String, Any?>>()
  private val onSize by EventDispatcher<Map<String, Any?>>()
  private val onStats by EventDispatcher<Map<String, Any?>>()
  private val main = Handler(Looper.getMainLooper())
  private val surfaceView = SurfaceView(context)
  private val mjpeg = MjpegPlayer(
    client = CLIENT,
    holder = { if (surfaceReady) surfaceView.holder else null },
    onSize = { w, h -> main.post { onSize(mapOf("width" to w, "height" to h)) } },
    onFrame = { emitState("streaming") },
    onEnded = { main.post { lost() } },
  )
  private val decoder = H264Decoder(
    onSize = { w, h -> main.post { onSize(mapOf("width" to w, "height" to h)) } },
    needKeyFrame = { requestKeyFrame() },
  )
  private val avcc = AvccPlayer(
    client = CLIENT,
    decoder = decoder,
    onFrame = { emitState("streaming") },
    onEnded = { main.post { lost() } },
  )

  private var surfaceReady = false
  private var socket: WebSocket? = null
  private var generation = 0
  private var lastKeyRequestMs = 0L
  private var lastAvccReconnectMs = 0L
  private var firstFrame = true
  private var connectPending = false
  private val retry = Runnable { connect() }

  // How the link carries the H.264 stream (see takeStats); socket thread writes, main reads.
  private val statsLock = Any()
  private var statsBytes = 0L
  private var statsFrames = 0
  private var statsSinceNs = System.nanoTime()
  private var offsetSum = 0.0
  private var offsetCount = 0
  private var offsetMin = Double.MAX_VALUE
  private var offsetMax = -Double.MAX_VALUE
  private val recentMins = ArrayDeque<Double>()
  private var lastArrivalMs = 0.0
  // The hub sends something every half second even for a still screen, so a
  // link silent for three reports (6 s) is dead, for instance one opened while
  // the device was still booting: it is dropped and opened again.
  private var silentReports = 0
  private val statsTick = object : Runnable {
    override fun run() {
      val stats = when (mode) {
        "mjpeg" -> mjpeg.takeStats()
        "avcc" -> avcc.takeStats()
        else -> takeStats()
      }
      silentReports = if (((stats["kbps"] as? Double) ?: 0.0) > 0.0) 0 else silentReports + 1
      if (silentReports >= 3) {
        silentReports = 0
        disconnect()
        lost()
        return
      }
      onStats(stats + ("mode" to mode))
      main.postDelayed(this, STATS_MS)
    }
  }

  /** "h264" (the emulator's WebSocket video) or "mjpeg" (the simulator's multipart stream). */
  var mode: String = "h264"
    set(value) {
      if (field != value) {
        field = value
        scheduleConnect()
      }
    }
  var url: String? = null
    set(value) {
      if (field != value) {
        field = value
        scheduleConnect()
      }
    }
  var headers: Map<String, String> = emptyMap()
    set(value) {
      field = value
      scheduleConnect()
    }

  init {
    surfaceView.holder.addCallback(this)
    addView(surfaceView, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
  }

  override fun surfaceCreated(holder: SurfaceHolder) {
    surfaceReady = true
    if (mode == "h264") decoder.setSurface(holder.surface)
    scheduleConnect()
  }

  override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) = Unit

  override fun surfaceDestroyed(holder: SurfaceHolder) {
    surfaceReady = false
    disconnect()
    decoder.setSurface(null)
    mjpeg.stop()
  }

  override fun onDetachedFromWindow() {
    super.onDetachedFromWindow()
    disconnect()
    decoder.release()
    mjpeg.stop()
  }

  /** Url and headers arrive as separate props: connect once both have landed. */
  private fun scheduleConnect() {
    if (connectPending) return
    connectPending = true
    main.post {
      connectPending = false
      connect()
    }
  }

  private fun connect() {
    disconnect()
    silentReports = 0
    // An empty url is the screen saying "no stream for now" (a device that is off).
    val target = url?.takeIf { it.isNotBlank() } ?: return
    if (!surfaceReady) return
    val mine = ++generation
    firstFrame = true
    emitState("connecting")
    when (mode) { // start the first window now
      "mjpeg" -> mjpeg.takeStats()
      "avcc" -> avcc.takeStats()
      else -> takeStats()
    }
    main.removeCallbacks(statsTick)
    main.postDelayed(statsTick, STATS_MS)
    if (mode == "mjpeg") {
      mjpeg.start(target, headers)
      return
    }
    decoder.setSurface(surfaceView.holder.surface)
    if (mode == "avcc") {
      avcc.start(target, headers)
      return
    }
    val request = Request.Builder().url(target).apply { headers.forEach { (name, value) -> addHeader(name, value) } }.build()
    socket = CLIENT.newWebSocket(request, object : WebSocketListener() {
      override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
        if (mine == generation) handleFrame(bytes)
      }

      override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
        if (mine == generation) lost()
      }

      override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
        if (mine == generation) lost()
      }
    })
  }

  private fun disconnect() {
    main.removeCallbacks(retry)
    avcc.stop()
    main.removeCallbacks(statsTick)
    mjpeg.stop()
    generation++
    socket?.cancel()
    socket = null
  }

  private fun lost() {
    emitState("closed")
    main.removeCallbacks(retry)
    main.postDelayed(retry, RETRY_MS)
  }

  private fun handleFrame(bytes: ByteString) {
    val data = bytes.toByteArray()
    var offset = 0
    // The hub's SEMU header (v1 is 16 bytes, v2 is 24) in front of the access unit.
    val arrivalMs = System.nanoTime() / 1e6
    synchronized(statsLock) { statsBytes += data.size }
    if (data.size > 24 && data[0].toInt() == 0x53 && data[1].toInt() == 0x45 && data[2].toInt() == 0x4d && data[3].toInt() == 0x55) {
      offset = if (data[4].toInt() == 1) 16 else 24
      // Bytes 8-15: the capture time in microseconds, big-endian.
      var captureUs = 0L
      for (i in 8 until 16) captureUs = (captureUs shl 8) or (data[i].toLong() and 0xff)
      val lateMs = arrivalMs - captureUs / 1000.0
      synchronized(statsLock) {
        // A still screen is refreshed every half second with a frame whose
        // capture time stays put: only frames in motion set the usual lateness.
        if (arrivalMs - lastArrivalMs < 300) {
          offsetSum += lateMs
          offsetCount++
          offsetMin = minOf(offsetMin, lateMs)
        }
        offsetMax = maxOf(offsetMax, lateMs)
        lastArrivalMs = arrivalMs
        statsFrames++
      }
    }
    decoder.queue(data, offset, data.size - offset)
    if (firstFrame) {
      firstFrame = false
      emitState("streaming")
    }
  }

  /**
   * How the link has carried the H.264 stream since the last call: kilobytes
   * per second, frames, `lagMs` (how much later than usual frames in motion
   * arrived after capture) and `maxLagMs` (the latest frame of all). A link
   * that cannot carry the bitrate queues frames, so the lag keeps growing.
   */
  private fun takeStats(): Map<String, Any> = synchronized(statsLock) {
    val now = System.nanoTime()
    val span = maxOf(now - statsSinceNs, 1_000_000L) / 1e9
    val stats = mutableMapOf<String, Any>("kbps" to statsBytes * 8 / 1000.0 / span, "frames" to statsFrames)
    if (offsetCount > 0) {
      recentMins.addLast(offsetMin)
      while (recentMins.size > 5) recentMins.removeFirst()
    }
    if (recentMins.isNotEmpty()) {
      val floor = recentMins.min()
      if (offsetCount >= 3) stats["lagMs"] = maxOf(0.0, offsetSum / offsetCount - floor)
      if (statsFrames > 0) stats["maxLagMs"] = maxOf(0.0, offsetMax - floor)
    }
    statsBytes = 0
    statsFrames = 0
    offsetSum = 0.0
    offsetCount = 0
    offsetMin = Double.MAX_VALUE
    offsetMax = -Double.MAX_VALUE
    statsSinceNs = now
    stats
  }

  private fun requestKeyFrame() {
    val now = System.currentTimeMillis()
    if (now - lastKeyRequestMs < KEY_REQUEST_GAP_MS) return
    lastKeyRequestMs = now
    if (mode == "avcc") {
      // The simulator's HTTP stream takes no requests; a new connection starts
      // with a key frame. Reconnect at once: the last picture stays up meanwhile.
      if (now - lastAvccReconnectMs < AVCC_RECONNECT_GAP_MS) return
      lastAvccReconnectMs = now
      main.post { connect() }
      return
    }
    socket?.send("{\"type\":\"reset-video\"}")
  }

  private fun emitState(state: String) {
    main.post { onState(mapOf("state" to state)) }
  }

  private companion object {
    const val RETRY_MS = 1_500L
    const val STATS_MS = 2_000L
    const val AVCC_RECONNECT_GAP_MS = 3_000L
    const val KEY_REQUEST_GAP_MS = 1_000L
    val CLIENT: OkHttpClient = OkHttpClient.Builder()
      .readTimeout(0, TimeUnit.MILLISECONDS)
      .pingInterval(15, TimeUnit.SECONDS)
      .build()
  }
}
