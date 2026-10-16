import AppKit
import OpenGL.GL3
import SwiftUI

struct MPVTrack: Identifiable, Hashable {
    var id: Int
    var type: String // video, audio, sub
    var title: String
    var selected: Bool
}

/// Owns an mpv handle. Events are drained on a private queue and published on the main actor.
final class MPVController {
    private(set) var handle: OpaquePointer?
    private let queue = DispatchQueue(label: "wrentv.mpv.events")

    var onTime: ((Double, Double) -> Void)?
    var onPause: ((Bool) -> Void)?
    var onEnd: (() -> Void)?
    var onTracks: (([MPVTrack]) -> Void)?
    var onBuffering: ((Bool) -> Void)?
    var onError: ((String) -> Void)?

    private var position = 0.0
    private var duration = 0.0

    init() {
        handle = mpv_create()
        guard let handle else { return }
        let opts: [(String, String)] = [
            ("vo", "libmpv"),
            ("hwdec", "videotoolbox"),
            ("keep-open", "yes"),
            ("idle", "yes"),
            ("input-default-bindings", "no"),
            ("input-vo-keyboard", "no"),
            ("osc", "no"),
            ("terminal", "no"),
            ("msg-level", "all=warn"),
            ("cache", "yes"),
            ("demuxer-max-bytes", "400MiB"),
            ("demuxer-readahead-secs", "60"),
            ("user-agent", "WrenTV/0.1"),
            ("sub-font-size", "44"),
            ("sub-border-size", "2.5"),
            ("audio-channels", "auto"),
            ("alang", "eng,en"),
            ("slang", "eng,en"),
        ]
        for (k, v) in opts { mpv_set_option_string(handle, k, v) }
        mpv_initialize(handle)
        mpv_observe_property(handle, 0, "time-pos", MPV_FORMAT_DOUBLE)
        mpv_observe_property(handle, 0, "duration", MPV_FORMAT_DOUBLE)
        mpv_observe_property(handle, 0, "pause", MPV_FORMAT_FLAG)
        mpv_observe_property(handle, 0, "eof-reached", MPV_FORMAT_FLAG)
        mpv_observe_property(handle, 0, "paused-for-cache", MPV_FORMAT_FLAG)
        mpv_observe_property(handle, 0, "track-list/count", MPV_FORMAT_INT64)
        mpv_set_wakeup_callback(handle, { ctx in
            let c = Unmanaged<MPVController>.fromOpaque(ctx!).takeUnretainedValue()
            c.queue.async { c.drain() }
        }, Unmanaged.passUnretained(self).toOpaque())
    }

    func load(_ url: URL, start: Double) {
        if start > 1 { set("start", "+\(start)") }
        command("loadfile", url.absoluteString)
    }

    func command(_ args: String...) {
        guard let handle else { return }
        var cargs: [UnsafePointer<CChar>?] = args.map { UnsafePointer(strdup($0)) }
        cargs.append(nil)
        mpv_command(handle, &cargs)
        for p in cargs { free(UnsafeMutablePointer(mutating: p)) }
    }

    func set(_ name: String, _ value: String) {
        guard let handle else { return }
        mpv_set_property_string(handle, name, value)
    }

    func string(_ name: String) -> String? {
        guard let handle, let c = mpv_get_property_string(handle, name) else { return nil }
        defer { mpv_free(c) }
        return String(cString: c)
    }

    var currentTime: Double { position }
    var currentDuration: Double { duration }

    func togglePause() { command("cycle", "pause") }
    func seek(to t: Double) { command("seek", String(t), "absolute+keyframes") }
    func seek(by d: Double) { command("seek", String(d), "relative") }
    func selectTrack(_ t: MPVTrack) { set(t.type == "audio" ? "aid" : "sid", String(t.id)) }
    func disableSubtitles() { set("sid", "no") }

    /// Must be called after the render layer has been released.
    func destroy() {
        queue.sync {
            guard let h = handle else { return }
            mpv_set_wakeup_callback(h, nil, nil)
            handle = nil
            mpv_terminate_destroy(h)
        }
    }

    private func drain() {
        while let handle {
            guard let ev = mpv_wait_event(handle, 0) else { return }
            let e = ev.pointee
            switch e.event_id {
            case MPV_EVENT_NONE:
                return
            case MPV_EVENT_PROPERTY_CHANGE:
                guard let prop = e.data?.assumingMemoryBound(to: mpv_event_property.self).pointee else { continue }
                handleProperty(prop)
            case MPV_EVENT_FILE_LOADED:
                publishTracks()
            case MPV_EVENT_END_FILE:
                if let d = e.data?.assumingMemoryBound(to: mpv_event_end_file.self).pointee, d.reason == MPV_END_FILE_REASON_ERROR {
                    let msg = String(cString: mpv_error_string(d.error))
                    DispatchQueue.main.async { self.onError?(msg) }
                }
            case MPV_EVENT_SHUTDOWN:
                return
            default:
                continue
            }
        }
    }

    private func handleProperty(_ p: mpv_event_property) {
        let name = String(cString: p.name)
        switch (name, p.format) {
        case ("time-pos", MPV_FORMAT_DOUBLE):
            position = p.data.assumingMemoryBound(to: Double.self).pointee
            let (t, d) = (position, duration)
            DispatchQueue.main.async { self.onTime?(t, d) }
        case ("duration", MPV_FORMAT_DOUBLE):
            duration = p.data.assumingMemoryBound(to: Double.self).pointee
        case ("pause", MPV_FORMAT_FLAG):
            let v = p.data.assumingMemoryBound(to: Int32.self).pointee != 0
            DispatchQueue.main.async { self.onPause?(v) }
        case ("paused-for-cache", MPV_FORMAT_FLAG):
            let v = p.data.assumingMemoryBound(to: Int32.self).pointee != 0
            DispatchQueue.main.async { self.onBuffering?(v) }
        case ("eof-reached", MPV_FORMAT_FLAG):
            if p.data.assumingMemoryBound(to: Int32.self).pointee != 0 {
                DispatchQueue.main.async { self.onEnd?() }
            }
        case ("track-list/count", _):
            publishTracks()
        default:
            break
        }
    }

    private func publishTracks() {
        let n = Int(string("track-list/count") ?? "0") ?? 0
        var tracks: [MPVTrack] = []
        for i in 0..<n {
            let p = "track-list/\(i)/"
            guard let type = string(p + "type"), type != "video", let id = Int(string(p + "id") ?? "") else { continue }
            var parts: [String] = []
            if let lang = string(p + "lang"), !lang.isEmpty {
                parts.append(Locale.current.localizedString(forLanguageCode: lang) ?? lang)
            }
            if let t = string(p + "title"), !t.isEmpty { parts.append(t) }
            if let c = string(p + "codec") { parts.append(c.uppercased()) }
            if string(p + "forced") == "yes" { parts.append("Forced") }
            tracks.append(MPVTrack(id: id, type: type, title: parts.joined(separator: " · "), selected: string(p + "selected") == "yes"))
        }
        DispatchQueue.main.async { self.onTracks?(tracks) }
    }
}

// MARK: - Rendering

private func mpvGetProcAddress(_ ctx: UnsafeMutableRawPointer?, _ name: UnsafePointer<CChar>?) -> UnsafeMutableRawPointer? {
    guard let name else { return nil }
    let symbol = CFStringCreateWithCString(kCFAllocatorDefault, name, CFStringBuiltInEncodings.ASCII.rawValue)
    let bundle = CFBundleGetBundleWithIdentifier("com.apple.opengl" as CFString)
    return CFBundleGetFunctionPointerForName(bundle, symbol)
}

/// OpenGL layer driven by mpv's render API.
final class MPVLayer: CAOpenGLLayer {
    private weak var controller: MPVController?
    private var renderCtx: OpaquePointer?
    private var glCtx: CGLContextObj?
    /// Called once the render context exists (vo=libmpv needs it before loading a file).
    var onReady: (() -> Void)?

    init(controller: MPVController) {
        self.controller = controller
        super.init()
        isAsynchronous = false
        needsDisplayOnBoundsChange = true
        autoresizingMask = [.layerWidthSizable, .layerHeightSizable]
        backgroundColor = NSColor.black.cgColor
    }

    override init(layer: Any) {
        let l = layer as! MPVLayer
        controller = l.controller
        super.init(layer: layer)
    }

    required init?(coder: NSCoder) { fatalError() }

    override func copyCGLPixelFormat(forDisplayMask mask: UInt32) -> CGLPixelFormatObj {
        let attrs: [CGLPixelFormatAttribute] = [
            kCGLPFAOpenGLProfile, CGLPixelFormatAttribute(UInt32(kCGLOGLPVersion_3_2_Core.rawValue)),
            kCGLPFADoubleBuffer, kCGLPFAAccelerated, kCGLPFAAllowOfflineRenderers,
            kCGLPFASupportsAutomaticGraphicsSwitching,
            CGLPixelFormatAttribute(0),
        ]
        var pix: CGLPixelFormatObj?
        var n: GLint = 0
        CGLChoosePixelFormat(attrs, &pix, &n)
        return pix!
    }

    override func copyCGLContext(forPixelFormat pf: CGLPixelFormatObj) -> CGLContextObj {
        let ctx = super.copyCGLContext(forPixelFormat: pf)
        var swap: GLint = 1
        CGLSetParameter(ctx, kCGLCPSwapInterval, &swap)
        CGLSetCurrentContext(ctx)
        glCtx = ctx
        createRenderContext()
        return ctx
    }

    private func createRenderContext() {
        guard renderCtx == nil, let handle = controller?.handle else { return }
        var initParams = mpv_opengl_init_params(get_proc_address: mpvGetProcAddress, get_proc_address_ctx: nil)
        let api = strdup(MPV_RENDER_API_TYPE_OPENGL)
        defer { free(api) }
        withUnsafeMutablePointer(to: &initParams) { ip in
            var params = [
                mpv_render_param(type: MPV_RENDER_PARAM_API_TYPE, data: api),
                mpv_render_param(type: MPV_RENDER_PARAM_OPENGL_INIT_PARAMS, data: ip),
                mpv_render_param(),
            ]
            mpv_render_context_create(&renderCtx, handle, &params)
        }
        mpv_render_context_set_update_callback(renderCtx, { ctx in
            let layer = Unmanaged<MPVLayer>.fromOpaque(ctx!).takeUnretainedValue()
            DispatchQueue.main.async { layer.setNeedsDisplay() }
        }, Unmanaged.passUnretained(self).toOpaque())
        if let onReady {
            self.onReady = nil
            DispatchQueue.main.async(execute: onReady)
        }
    }

    override func canDraw(inCGLContext ctx: CGLContextObj, pixelFormat pf: CGLPixelFormatObj, forLayerTime t: CFTimeInterval, displayTime ts: UnsafePointer<CVTimeStamp>?) -> Bool {
        true
    }

    override func draw(inCGLContext ctx: CGLContextObj, pixelFormat pf: CGLPixelFormatObj, forLayerTime t: CFTimeInterval, displayTime ts: UnsafePointer<CVTimeStamp>?) {
        guard let renderCtx else {
            glClearColor(0, 0, 0, 1)
            glClear(GLbitfield(GL_COLOR_BUFFER_BIT))
            return
        }
        var fbo: GLint = 0
        glGetIntegerv(GLenum(GL_DRAW_FRAMEBUFFER_BINDING), &fbo)
        var vp = [GLint](repeating: 0, count: 4)
        glGetIntegerv(GLenum(GL_VIEWPORT), &vp)
        var target = mpv_opengl_fbo(fbo: Int32(fbo), w: vp[2], h: vp[3], internal_format: 0)
        var flip: CInt = 1
        withUnsafeMutablePointer(to: &target) { tp in
            withUnsafeMutablePointer(to: &flip) { fp in
                var params = [
                    mpv_render_param(type: MPV_RENDER_PARAM_OPENGL_FBO, data: tp),
                    mpv_render_param(type: MPV_RENDER_PARAM_FLIP_Y, data: fp),
                    mpv_render_param(),
                ]
                mpv_render_context_render(renderCtx, &params)
            }
        }
        glFlush()
    }

    /// Frees the render context; must happen before the mpv handle is destroyed.
    func teardown() {
        guard let rc = renderCtx else { return }
        mpv_render_context_set_update_callback(rc, nil, nil)
        if let glCtx { CGLSetCurrentContext(glCtx) }
        mpv_render_context_free(rc)
        renderCtx = nil
    }
}

final class MPVNSView: NSView {
    let mpvLayer: MPVLayer

    init(controller: MPVController) {
        mpvLayer = MPVLayer(controller: controller)
        super.init(frame: .zero)
        wantsLayer = true
    }

    required init?(coder: NSCoder) { fatalError() }

    override func makeBackingLayer() -> CALayer { mpvLayer }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        mpvLayer.contentsScale = window?.backingScaleFactor ?? 2
    }
}

struct MPVVideoView: NSViewRepresentable {
    let controller: MPVController
    let onCreate: (MPVLayer) -> Void

    func makeNSView(context: Context) -> MPVNSView {
        let v = MPVNSView(controller: controller)
        onCreate(v.mpvLayer)
        return v
    }

    func updateNSView(_ nsView: MPVNSView, context: Context) {}
}
