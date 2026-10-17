@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.content.Context
import android.hardware.display.DisplayManager
import android.media.MediaCodecList
import android.view.Display
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.audio.AudioCapabilities
import com.wrentv.tv.data.ClientCaps

/**
 * What this TV can play without the server converting it: decoders from
 * MediaCodec, HDR and Dolby Vision from the display, and the surround formats
 * the TV or receiver takes as passthrough.
 */
object DeviceCaps {
    private val videoTypes = mapOf(
        "video/avc" to "h264",
        "video/hevc" to "hevc",
        "video/av01" to "av1",
        "video/x-vnd.on2.vp9" to "vp9",
    )
    private val audioTypes = mapOf(
        "audio/mp4a-latm" to "aac",
        "audio/ac3" to "ac3",
        "audio/eac3" to "eac3",
        "audio/eac3-joc" to "eac3",
        "audio/true-hd" to "truehd",
        "audio/vnd.dts" to "dts",
        "audio/vnd.dts.hd" to "dts",
        "audio/flac" to "flac",
        "audio/opus" to "opus",
        "audio/mpeg" to "mp3",
    )
    private val passthrough = mapOf(
        C.ENCODING_AC3 to "ac3",
        C.ENCODING_E_AC3 to "eac3",
        C.ENCODING_E_AC3_JOC to "eac3",
        C.ENCODING_DTS to "dts",
        C.ENCODING_DTS_HD to "dts",
        C.ENCODING_DOLBY_TRUEHD to "truehd",
    )

    private var decoders: Set<String>? = null

    /**
     * Passthrough is checked on every call, not cached: it follows the audio
     * output, so turning a receiver off or switching inputs changes it.
     * [exclude] drops audio codecs that already failed to play here.
     */
    fun current(context: Context, exclude: Set<String> = emptySet()): ClientCaps {
        val types = decoders ?: MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos.filter { !it.isEncoder }
            .flatMap { it.supportedTypes.map(String::lowercase) }.toSet().also { decoders = it }
        val video = videoTypes.filterKeys { it in types }.values.distinct()
        val audio = audioTypes.filterKeys { it in types }.values.toMutableSet()
        runCatching {
            val caps = AudioCapabilities.getCapabilities(context, AudioAttributes.DEFAULT, null)
            passthrough.forEach { (encoding, codec) -> if (caps.supportsEncoding(encoding)) audio += codec }
        }
        audio -= exclude

        @Suppress("DEPRECATION")
        val hdrTypes = context.getSystemService(DisplayManager::class.java)?.getDisplay(Display.DEFAULT_DISPLAY)
            ?.hdrCapabilities?.supportedHdrTypes?.toSet().orEmpty()
        val hdr = hdrTypes.any { it == Display.HdrCapabilities.HDR_TYPE_HDR10 || it == Display.HdrCapabilities.HDR_TYPE_HDR10_PLUS }
        val dolbyVision = Display.HdrCapabilities.HDR_TYPE_DOLBY_VISION in hdrTypes && "video/dolby-vision" in types

        return ClientCaps(
            engines = listOf("avplayer"),
            nativeVideo = video,
            nativeAudio = audio.toList().sorted(),
            // ExoPlayer reads Matroska and MPEG-TS itself, so those play directly too.
            nativeContainers = listOf("mp4", "mov", "mkv", "ts"),
            dolbyVision = dolbyVision,
            hdr = hdr || dolbyVision,
            // Android hands HDR frames to the display as they are; on an SDR TV
            // (or an HDMI input without HDR turned on) they look washed out.
            needsSdr = !hdr && !dolbyVision,
            // Not FLAC: Android's decoder fails where each HLS segment's
            // separately encoded FLAC stream restarts its frame numbers.
            audioFallback = "aac",
        )
    }
}
