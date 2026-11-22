package ao.mobile.devicestream

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Paint
import android.graphics.Rect
import android.view.SurfaceHolder
import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException

/**
 * Plays the simulator's multipart/x-mixed-replace MJPEG stream onto a Surface.
 * One thread reads parts off the socket and keeps only the newest; another
 * decodes and draws it. A phone that cannot keep up therefore skips frames
 * instead of falling behind, which is what keeps taps and swipes feeling live.
 */
class MjpegPlayer(
  private val client: OkHttpClient,
  private val holder: () -> SurfaceHolder?,
  private val onSize: (Int, Int) -> Unit,
  private val onFrame: () -> Unit,
  private val onEnded: () -> Unit,
) {
  private val lock = Object()
  private val paint = Paint(Paint.FILTER_BITMAP_FLAG)
  private var latest: ByteArray? = null
  private var call: Call? = null
  private var reader: Thread? = null
  private var decoder: Thread? = null

  @Volatile private var running = false
  private val statsLock = Any()
  private var statsBytes = 0L
  private var statsFrames = 0
  private var statsBusyNs = 0L
  private var statsSinceNs = System.nanoTime()
  private var width = 0
  private var height = 0

  fun start(url: String, headers: Map<String, String>) {
    stop()
    running = true
    val request = Request.Builder().url(url).apply { headers.forEach { (name, value) -> addHeader(name, value) } }.build()
    val live = client.newCall(request)
    call = live
    reader = Thread({ read(live) }, "device-mjpeg-read").also { it.isDaemon = true; it.start() }
    decoder = Thread({ decode() }, "device-mjpeg-decode").also { it.isDaemon = true; it.start() }
  }

  fun stop() {
    running = false
    call?.cancel()
    call = null
    synchronized(lock) {
      latest = null
      lock.notifyAll()
    }
    reader = null
    decoder = null
  }

  private fun read(live: Call) {
    try {
      live.execute().use { response ->
        if (!response.isSuccessful) throw IOException("stream ${response.code}")
        val source = response.body?.source() ?: throw IOException("empty stream")
        var previous: ByteArray? = null
        while (running) {
          val line = source.readUtf8LineStrict()
          if (!line.startsWith("--")) continue
          var length = -1
          while (true) {
            val header = source.readUtf8LineStrict()
            if (header.isEmpty()) break
            if (header.startsWith("Content-Length:", ignoreCase = true)) length = header.substring(15).trim().toIntOrNull() ?: -1
          }
          if (length <= 0 || length > MAX_PART) throw IOException("bad part length $length")
          val partStart = System.nanoTime()
          val bytes = source.readByteArray(length.toLong())
          synchronized(statsLock) {
            statsBusyNs += System.nanoTime() - partStart
            statsBytes += length
            statsFrames++
          }
          // The simulator repeats an unchanged screen: decoding and drawing the
          // same picture again is work with nothing to show.
          if (previous != null && previous.contentEquals(bytes)) continue
          previous = bytes
          synchronized(lock) {
            latest = bytes // an unshown older frame is simply replaced
            lock.notifyAll()
          }
        }
      }
    } catch (_: Exception) {
      // Closed by stop() or lost: either way the view decides what happens next.
    } finally {
      if (running) {
        running = false
        onEnded()
      }
    }
  }

  /**
   * How the link has carried the stream since the last call: kilobytes per
   * second, parts, and `busy`, the share of the time spent receiving a part.
   * The hub sends the newest frame as soon as the last one is through, so a
   * link that cannot carry the stream is busy nearly all the time.
   */
  fun takeStats(): Map<String, Any> = synchronized(statsLock) {
    val now = System.nanoTime()
    val span = maxOf(now - statsSinceNs, 1_000_000L).toDouble()
    val stats = mapOf(
      "kbps" to statsBytes * 8 / 1000.0 / (span / 1e9),
      "frames" to statsFrames,
      "busy" to minOf(1.0, statsBusyNs / span),
    )
    statsBytes = 0
    statsFrames = 0
    statsBusyNs = 0
    statsSinceNs = now
    stats
  }

  private fun decode() {
    var reusable: Bitmap? = null
    var first = true
    while (running) {
      val data = synchronized(lock) {
        while (latest == null && running) lock.wait()
        val taken = latest
        latest = null
        taken
      } ?: continue
      val options = BitmapFactory.Options().apply {
        inMutable = true
        inBitmap = reusable
      }
      val bitmap = try {
        BitmapFactory.decodeByteArray(data, 0, data.size, options)
      } catch (_: IllegalArgumentException) {
        // The recycled bitmap no longer fits (the picture changed size): decode into a new one.
        runCatching { BitmapFactory.decodeByteArray(data, 0, data.size) }.getOrNull()
      } ?: continue
      reusable = bitmap
      if (bitmap.width != width || bitmap.height != height) {
        width = bitmap.width
        height = bitmap.height
        onSize(width, height)
      }
      draw(bitmap)
      if (first) {
        first = false
        onFrame()
      }
    }
  }

  private fun draw(bitmap: Bitmap) {
    val surface = holder() ?: return
    val canvas = surface.lockHardwareCanvas() ?: return
    try {
      canvas.drawBitmap(bitmap, null, Rect(0, 0, canvas.width, canvas.height), paint)
    } finally {
      surface.unlockCanvasAndPost(canvas)
    }
  }

  private companion object {
    const val MAX_PART = 16 shl 20
  }
}
