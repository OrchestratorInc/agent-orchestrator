package ao.mobile.devicestream

import android.media.MediaCodec
import android.media.MediaFormat
import android.os.Build
import android.view.Surface
import java.nio.ByteBuffer

/**
 * Decodes the device hub's H.264 access units straight onto a Surface with the
 * hardware decoder. Frames arrive one access unit at a time, key frames carrying
 * their SPS and PPS; the decoder starts on the first of those and, if it ever
 * falls behind or errors, waits for the next key frame (asking for one) rather
 * than showing a smeared picture.
 */
class H264Decoder(
  private val onSize: (Int, Int) -> Unit,
  private val needKeyFrame: () -> Unit,
) {
  private val lock = Any()
  private var surface: Surface? = null
  private var codec: MediaCodec? = null
  private var drain: Thread? = null

  @Volatile private var running = false
  private var width = 0
  private var height = 0
  private var waitingForKey = true
  private var ptsUs = 0L

  fun setSurface(next: Surface?) {
    synchronized(lock) {
      if (surface === next) return
      release()
      surface = next
    }
  }

  fun release() {
    synchronized(lock) {
      running = false
      val thread = drain
      drain = null
      val c = codec
      codec = null
      waitingForKey = true
      // Stopping first ends the drain thread's wait at once, so the join is short.
      c?.let { runCatching { it.stop() } }
      thread?.let { runCatching { it.join(100) } }
      c?.let { runCatching { it.release() } }
    }
  }

  /**
   * Decodes one Annex-B access unit. [waitUs] is how long to wait for a free
   * input buffer before counting the decoder as behind: short on the socket's
   * thread, longer for a reader of its own that may simply slow down.
   */
  fun queue(data: ByteArray, offset: Int, length: Int, waitUs: Long = INPUT_TIMEOUT_US) {
    val nals = AnnexB.split(data, offset, length)
    val sps = nals.firstOrNull { it.type == AnnexB.NAL_SPS }
    val pps = nals.firstOrNull { it.type == AnnexB.NAL_PPS }
    val isKey = nals.any { it.type == AnnexB.NAL_IDR }
    val c = synchronized(lock) {
      if (surface == null) return
      if (sps != null && pps != null) {
        val size = AnnexB.parseSps(data, sps)
        if (size != null && (codec == null || size.width != width || size.height != height)) {
          start(data, sps, pps, size)
        }
      }
      val current = codec
      if (current == null || (waitingForKey && !isKey)) {
        needKeyFrame()
        return
      }
      current
    }
    // Waiting for a free input buffer happens outside the lock: setSurface and
    // release run on the main thread and must not wait behind a slow decoder.
    val index = try {
      c.dequeueInputBuffer(waitUs)
    } catch (_: IllegalStateException) {
      failed(c)
      return
    } catch (_: MediaCodec.CodecException) {
      failed(c)
      return
    }
    synchronized(lock) {
      // Released or replaced while waiting: its buffer went with it.
      if (codec !== c) return
      try {
        if (index < 0) {
          // The decoder is behind: drop to the next key frame instead of a broken picture.
          waitingForKey = true
          needKeyFrame()
          return
        }
        val buffer = c.getInputBuffer(index) ?: return
        buffer.clear()
        if (length > buffer.capacity()) {
          c.queueInputBuffer(index, 0, 0, ptsUs, 0)
          waitingForKey = true
          needKeyFrame()
          return
        }
        buffer.put(data, offset, length)
        c.queueInputBuffer(index, 0, length, ptsUs, 0)
        ptsUs += FRAME_US
        if (isKey) waitingForKey = false
      } catch (_: IllegalStateException) {
        release()
        needKeyFrame()
      } catch (_: MediaCodec.CodecException) {
        release()
        needKeyFrame()
      }
    }
  }

  /** A codec that broke while dequeuing: start over at the next key frame, unless it was already replaced. */
  private fun failed(c: MediaCodec) {
    synchronized(lock) {
      if (codec !== c) return
      release()
    }
    needKeyFrame()
  }

  private fun start(data: ByteArray, sps: Nal, pps: Nal, size: SpsSize) {
    release()
    val target = surface ?: return
    val format = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, size.width, size.height)
    format.setByteBuffer("csd-0", ByteBuffer.wrap(withStartCode(data, sps)))
    format.setByteBuffer("csd-1", ByteBuffer.wrap(withStartCode(data, pps)))
    format.setInteger(MediaFormat.KEY_PRIORITY, 0) // real time
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) format.setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
    val c = MediaCodec.createDecoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
    try {
      c.configure(format, target, null, 0)
      c.start()
    } catch (e: Exception) {
      runCatching { c.release() }
      return
    }
    codec = c
    width = size.width
    height = size.height
    waitingForKey = true
    running = true
    drain = Thread({
      val info = MediaCodec.BufferInfo()
      while (running) {
        try {
          val index = c.dequeueOutputBuffer(info, OUTPUT_TIMEOUT_US)
          if (index >= 0) c.releaseOutputBuffer(index, true)
        } catch (_: Exception) {
          break
        }
      }
    }, "device-stream-drain").also { it.isDaemon = true; it.start() }
    onSize(size.width, size.height)
  }

  private fun withStartCode(data: ByteArray, nal: Nal): ByteArray {
    val out = ByteArray(4 + nal.end - nal.start)
    out[3] = 1
    System.arraycopy(data, nal.start, out, 4, nal.end - nal.start)
    return out
  }

  private companion object {
    const val INPUT_TIMEOUT_US = 8_000L
    const val OUTPUT_TIMEOUT_US = 10_000L
    const val FRAME_US = 16_667L
  }
}
