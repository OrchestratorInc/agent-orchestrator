package ao.mobile.devicestream

/** One NAL unit inside an Annex-B access unit: [start, end) is the unit without its start code. */
data class Nal(val type: Int, val start: Int, val end: Int)

/** Width and height of the coded picture, after cropping. */
data class SpsSize(val width: Int, val height: Int)

/** Just enough H.264 parsing to find the parameter sets and the picture size. */
object AnnexB {
  const val NAL_IDR = 5
  const val NAL_SPS = 7
  const val NAL_PPS = 8

  /** Splits an Annex-B buffer (3- or 4-byte start codes) into its NAL units. */
  fun split(data: ByteArray, offset: Int = 0, length: Int = data.size - offset): List<Nal> {
    val end = offset + length
    val starts = ArrayList<Int>() // index of the first byte after each start code
    var i = offset
    while (i + 2 < end) {
      if (data[i].toInt() == 0 && data[i + 1].toInt() == 0 && data[i + 2].toInt() == 1) {
        starts.add(i + 3)
        i += 3
      } else {
        i++
      }
    }
    val out = ArrayList<Nal>(starts.size)
    for ((index, start) in starts.withIndex()) {
      var stop = if (index + 1 < starts.size) starts[index + 1] - 3 else end
      // A 4-byte start code leaves one zero byte on the previous unit.
      while (stop > start && data[stop - 1].toInt() == 0) stop--
      if (stop > start) out.add(Nal(data[start].toInt() and 0x1f, start, stop))
    }
    return out
  }

  /** Reads the picture size from an SPS NAL (first byte is the NAL header). Null when it cannot be parsed. */
  fun parseSps(data: ByteArray, nal: Nal): SpsSize? {
    return try {
      val rbsp = unescape(data, nal.start + 1, nal.end)
      val r = BitReader(rbsp)
      val profile = r.bits(8)
      r.bits(8) // constraint flags
      r.bits(8) // level
      r.ue() // sps id
      var chromaFormat = 1
      if (profile in HIGH_PROFILES) {
        chromaFormat = r.ue()
        if (chromaFormat == 3) r.bits(1)
        r.ue() // luma bit depth
        r.ue() // chroma bit depth
        r.bits(1) // transform bypass
        if (r.bits(1) == 1) {
          val lists = if (chromaFormat != 3) 8 else 12
          for (list in 0 until lists) {
            if (r.bits(1) == 1) skipScalingList(r, if (list < 6) 16 else 64)
          }
        }
      }
      r.ue() // log2_max_frame_num
      when (r.ue()) { // pic_order_cnt_type
        0 -> r.ue()
        1 -> {
          r.bits(1)
          r.se()
          r.se()
          repeat(r.ue()) { r.se() }
        }
      }
      r.ue() // max_num_ref_frames
      r.bits(1) // gaps
      val widthMbs = r.ue() + 1
      val heightMapUnits = r.ue() + 1
      val frameMbsOnly = r.bits(1)
      if (frameMbsOnly == 0) r.bits(1)
      r.bits(1) // direct_8x8_inference
      var cropLeft = 0
      var cropRight = 0
      var cropTop = 0
      var cropBottom = 0
      if (r.bits(1) == 1) {
        cropLeft = r.ue()
        cropRight = r.ue()
        cropTop = r.ue()
        cropBottom = r.ue()
      }
      val unitX = if (chromaFormat == 0) 1 else 2
      val unitY = (if (chromaFormat == 0) 1 else 2) * (2 - frameMbsOnly)
      val width = widthMbs * 16 - (cropLeft + cropRight) * unitX
      val height = (2 - frameMbsOnly) * heightMapUnits * 16 - (cropTop + cropBottom) * unitY
      if (width in 16..8192 && height in 16..8192) SpsSize(width, height) else null
    } catch (_: IndexOutOfBoundsException) {
      null
    }
  }

  private val HIGH_PROFILES = setOf(100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135)

  private fun skipScalingList(r: BitReader, size: Int) {
    var last = 8
    var next = 8
    for (j in 0 until size) {
      if (next != 0) {
        next = (last + r.se() + 256) % 256
      }
      last = if (next == 0) last else next
    }
  }

  /** Removes emulation-prevention bytes (00 00 03 -> 00 00). */
  private fun unescape(data: ByteArray, start: Int, end: Int): ByteArray {
    val out = java.io.ByteArrayOutputStream(end - start)
    var zeros = 0
    for (i in start until end) {
      val b = data[i].toInt() and 0xff
      if (zeros >= 2 && b == 3) {
        zeros = 0
        continue
      }
      out.write(b)
      zeros = if (b == 0) zeros + 1 else 0
    }
    return out.toByteArray()
  }

  private class BitReader(private val data: ByteArray) {
    private var pos = 0

    fun bits(count: Int): Int {
      var value = 0
      repeat(count) {
        val byte = data[pos shr 3].toInt() and 0xff
        value = (value shl 1) or ((byte shr (7 - (pos and 7))) and 1)
        pos++
      }
      return value
    }

    fun ue(): Int {
      var zeros = 0
      while (bits(1) == 0) {
        zeros++
        if (zeros > 31) throw IndexOutOfBoundsException()
      }
      return if (zeros == 0) 0 else (1 shl zeros) - 1 + bits(zeros)
    }

    fun se(): Int {
      val k = ue()
      return if (k and 1 == 1) (k + 1) / 2 else -(k / 2)
    }
  }
}
