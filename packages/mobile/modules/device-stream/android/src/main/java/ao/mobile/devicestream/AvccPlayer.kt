package ao.mobile.devicestream

import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.util.concurrent.atomic.AtomicReference

/**
 * Plays a simulator's H.264 stream (the hub's stream.avcc) through the hardware
 * decoder. It carries a scrolling simulator in ~5 Mbit/s where MJPEG needs ~112.
 *
 * The stream is length-prefixed packets: a 4-byte length (counting the type
 * byte), a type, then the payload. Type 1 is the decoder setup (an avcC
 * record), 2 and 3 key and delta frames as length-prefixed NAL units, which are
 * turned into the Annex-B access units [H264Decoder] takes (a key frame carries
 * the SPS and PPS, as on the emulator's stream). Type 4, a JPEG of the screen
 * sent first, is skipped: the decoder owns the surface and starts within a frame.
 * Playback is reported by the decoder's first picture, not by data arriving.
 */
class AvccPlayer(
  private val client: OkHttpClient,
  private val decoder: H264Decoder,
  /** The live connection ended on its own; [connection] is the number its start was given. */
  private val onEnded: (connection: Int) -> Unit,
) {
  /**
   * The live connection; a reader whose call is no longer this one has been
   * replaced or stopped. Installing, stopping and a reader's end all swap it
   * atomically, so a finishing reader can never clear its replacement's call.
   */
  private val call = AtomicReference<Call?>(null)

  private val statsLock = Any()
  private var statsBytes = 0L
  private var statsFrames = 0
  private var statsBusyNs = 0L
  private var statsSinceNs = System.nanoTime()

  fun start(url: String, headers: Map<String, String>, connection: Int) {
    val request = Request.Builder().url(url).apply { headers.forEach { (name, value) -> addHeader(name, value) } }.build()
    val live = client.newCall(request)
    call.getAndSet(live)?.cancel()
    Thread({ read(live, connection) }, "device-avcc-read").also { it.isDaemon = true; it.start() }
  }

  fun stop() {
    call.getAndSet(null)?.cancel()
  }

  /**
   * How the link has carried the stream since the last call: kilobytes per
   * second, frames, and `busy`, the share of the time spent receiving a packet.
   * The hub sends the next frame as soon as the last is through, so a link that
   * cannot carry the stream is busy nearly all the time.
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

  private fun read(live: Call, connection: Int) {
    try {
      live.execute().use { response ->
        if (!response.isSuccessful) throw IOException("stream ${response.code}")
        val source = response.body?.source() ?: throw IOException("empty stream")
        var parameterSets: ByteArray? = null
        while (call.get() === live) {
          val length = source.readInt()
          if (length < 1 || length > MAX_PACKET) throw IOException("bad packet length $length")
          val started = System.nanoTime()
          val type = source.readByte().toInt() and 0xff
          val payload = source.readByteArray((length - 1).toLong())
          synchronized(statsLock) {
            statsBusyNs += System.nanoTime() - started
            statsBytes += 4L + length
          }
          when (type) {
            1 -> parameterSets = annexBParameterSets(payload)
            2, 3 -> {
              val sets = parameterSets ?: continue
              val unit = toAnnexB(payload, if (type == 2) sets else null)
              // Waiting for the decoder only slows this read, and the hub
              // sends its next frame when this one is through, so nothing is
              // lost; dropping to a key frame would cost a reconnect.
              decoder.queue(unit, 0, unit.size, DECODER_WAIT_US)
              synchronized(statsLock) { statsFrames++ }
            }
          }
        }
      }
    } catch (_: Exception) {
      // Closed by stop() or lost: either way the view decides what happens next.
    } finally {
      // Only the reader that still owns the call reports its end.
      if (call.compareAndSet(live, null)) onEnded(connection)
    }
  }

  companion object {
    private const val MAX_PACKET = 16 shl 20
    private const val DECODER_WAIT_US = 250_000L
    private val START_CODE = byteArrayOf(0, 0, 0, 1)

    /** The SPS and PPS of an avcC record (ISO/IEC 14496-15), each behind a start code. */
    fun annexBParameterSets(record: ByteArray): ByteArray? {
      if (record.size < 7 || record[0].toInt() != 1) return null
      val out = java.io.ByteArrayOutputStream()
      var i = 5
      val spsCount = record[i].toInt() and 0x1f
      i++
      repeat(spsCount) {
        if (i + 2 > record.size) return null
        val n = ((record[i].toInt() and 0xff) shl 8) or (record[i + 1].toInt() and 0xff)
        i += 2
        if (i + n > record.size) return null
        out.write(START_CODE)
        out.write(record, i, n)
        i += n
      }
      if (i >= record.size) return null
      val ppsCount = record[i].toInt() and 0xff
      i++
      repeat(ppsCount) {
        if (i + 2 > record.size) return null
        val n = ((record[i].toInt() and 0xff) shl 8) or (record[i + 1].toInt() and 0xff)
        i += 2
        if (i + n > record.size) return null
        out.write(START_CODE)
        out.write(record, i, n)
        i += n
      }
      return out.toByteArray()
    }

    /** A frame's 4-byte-length NAL units as an Annex-B access unit, after [prefix] when given. */
    fun toAnnexB(payload: ByteArray, prefix: ByteArray?): ByteArray {
      val head = prefix?.size ?: 0
      val out = ByteArray(head + payload.size)
      prefix?.copyInto(out)
      var i = 0
      while (i + 4 <= payload.size) {
        val n = ((payload[i].toInt() and 0xff) shl 24) or ((payload[i + 1].toInt() and 0xff) shl 16) or
          ((payload[i + 2].toInt() and 0xff) shl 8) or (payload[i + 3].toInt() and 0xff)
        START_CODE.copyInto(out, head + i)
        val end = minOf(i + 4 + n, payload.size)
        payload.copyInto(out, head + i + 4, i + 4, end)
        i = end
      }
      return out
    }
  }
}
