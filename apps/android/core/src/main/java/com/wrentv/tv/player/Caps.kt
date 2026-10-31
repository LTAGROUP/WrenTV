@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.content.Context
import android.hardware.display.DisplayManager
import android.media.MediaCodecList
import android.os.Build
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

    /** "auto" (what the display reports), "on" or "off": set in Settings when a TV's HDR isn't detected. */
    fun hdrMode(context: Context): String =
        context.getSharedPreferences("wrentv", Context.MODE_PRIVATE).getString("hdrMode", "auto") ?: "auto"

    /**
     * Whether the display takes HDR10 and Dolby Vision. The display's own
     * capabilities can come back empty on newer Google TV devices and while
     * the current output mode is SDR, so every mode's HDR types count too
     * (Android 14+), and so does a display that is showing HDR right now.
     */
    fun displayHdr(context: Context, decoderTypes: Set<String>? = decoders): Pair<Boolean, Boolean> {
        val display = context.getSystemService(DisplayManager::class.java)?.getDisplay(Display.DEFAULT_DISPLAY)
            ?: return false to false
        val hdrTypes = buildSet {
            @Suppress("DEPRECATION")
            display.hdrCapabilities?.supportedHdrTypes?.let { addAll(it.toList()) }
            if (Build.VERSION.SDK_INT >= 34) {
                runCatching {
                    display.mode.supportedHdrTypes.forEach { add(it) }
                    display.supportedModes.forEach { m -> m.supportedHdrTypes.forEach { add(it) } }
                }
            }
        }
        val hdr = display.isHdr || hdrTypes.any {
            it == Display.HdrCapabilities.HDR_TYPE_HDR10 || it == Display.HdrCapabilities.HDR_TYPE_HDR10_PLUS
        }
        val dolbyVision = Display.HdrCapabilities.HDR_TYPE_DOLBY_VISION in hdrTypes &&
            decoderTypes?.contains("video/dolby-vision") == true
        return hdr to dolbyVision
    }

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

        val mode = hdrMode(context)
        val (detectedHdr, detectedDv) = displayHdr(context, types)
        val hdr = mode == "on" || (mode != "off" && detectedHdr)
        val dolbyVision = mode != "off" && detectedDv

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
