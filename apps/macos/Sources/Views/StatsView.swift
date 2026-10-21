import Charts
import SwiftUI

// The server's dashboard, as on the web: what's playing now and the
// machine's load, plays over time, the full history, each viewer and the
// libraries. The Mac and iOS apps share this file.

enum StatsTab: String, CaseIterable {
    case activity, overview, graphs, history, viewers, libraries

    var title: String { rawValue.capitalized }
}

/// Which tab is open, and where one tab sends another.
@MainActor @Observable
final class StatsNav {
    var tab = StatsTab(rawValue: UserDefaults.standard.string(forKey: "statsTab") ?? "") ?? .activity {
        didSet { UserDefaults.standard.set(tab.rawValue, forKey: "statsTab") }
    }
    /// The viewer open on the Viewers tab.
    var viewer: String?
    /// History's viewer filter, set when a viewer's "Full history" is followed.
    var historyUser = ""
    /// Overview and Graphs share one fetch per period.
    var stats: [Int: StatsResponse] = [:]

    func openViewer(_ id: String) {
        viewer = id
        tab = .viewers
    }
}

struct StatsView: View {
    @Environment(AppState.self) private var state
    #if os(iOS)
    @Environment(\.metrics) private var m
    @Environment(\.horizontalSizeClass) private var sizeClass
    #endif
    @AppStorage("statsDays") private var days = 30
    @State private var nav = StatsNav()

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                header
                switch nav.tab {
                case .activity: ActivityTab()
                case .overview: OverviewTab(days: days)
                case .graphs: GraphsTab(days: days)
                case .history: HistoryTab()
                case .viewers: ViewersTab()
                case .libraries: LibrariesTab()
                }
            }
            .padding(gutter)
        }
        .navigationTitle("Stats")
        .environment(nav)
        .environment(\.statsCompact, compact)
        #if os(iOS)
        .refreshable { state.refreshTick += 1 }
        #endif
    }

    private var gutter: CGFloat {
        #if os(iOS)
        m.gutter
        #else
        28
        #endif
    }

    private var compact: Bool {
        #if os(iOS)
        sizeClass == .compact
        #else
        false
        #endif
    }

    @ViewBuilder
    private var header: some View {
        let period = nav.tab == .overview || nav.tab == .graphs
        if compact {
            VStack(alignment: .leading, spacing: 10) {
                tabs
                if period { periodPicker }
            }
        } else {
            HStack(spacing: 16) {
                tabs
                Spacer(minLength: 0)
                if period { periodPicker.fixedSize() }
            }
        }
    }

    private var tabs: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(StatsTab.allCases, id: \.self) { t in
                    Button {
                        if t == .viewers && nav.tab == .viewers { nav.viewer = nil }
                        nav.tab = t
                    } label: {
                        Text(t.title)
                            .font(.callout.weight(.semibold))
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .foregroundStyle(nav.tab == t ? Color.black : Color.primary)
                            .background(nav.tab == t ? Color.white : Color.white.opacity(0.07), in: Capsule())
                            .contentShape(Capsule())
                    }
                    .buttonStyle(.plain)
                    .accessibilityAddTraits(nav.tab == t ? .isSelected : [])
                }
            }
        }
        .scrollClipDisabled()
    }

    private var periodPicker: some View {
        Picker("Period", selection: $days) {
            Text("7 days").tag(7)
            Text("30 days").tag(30)
            Text("90 days").tag(90)
            Text("1 year").tag(365)
        }
        .pickerStyle(.segmented)
        .labelsHidden()
    }
}

private struct StatsCompactKey: EnvironmentKey {
    static let defaultValue = false
}

private extension EnvironmentValues {
    /// A phone's width: tables become stacked rows.
    var statsCompact: Bool {
        get { self[StatsCompactKey.self] }
        set { self[StatsCompactKey.self] = newValue }
    }
}

// MARK: - Activity

private struct ActivityTab: View {
    @Environment(AppState.self) private var state
    @Environment(\.statsCompact) private var compact
    @AppStorage("statsSysRange") private var range = "1h"
    @State private var act: ActivityResponse?
    @State private var sys: SystemResponse?
    @State private var sysFailed = false
    /// Server clock minus ours, so positions move on from the server's reports.
    @State private var skew: Double = 0
    @State private var error: String?
    @State private var stopping: NowPlaying?
    @State private var stopMessage = ""
    @State private var stopError: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            if let act {
                KPIRow(items: totals(act.totals))
                nowPlaying(act.sessions)
            } else if let error {
                LoadFailed(message: error)
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 160)
            }
            HStack {
                Text("Server").font(.title3.bold())
                Spacer()
                Picker("Range", selection: $range) {
                    Text("1h").tag("1h")
                    Text("6h").tag("6h")
                    Text("24h").tag("24h")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
            }
            .padding(.top, 6)
            if let sys {
                SystemSection(sys: sys)
            } else if sysFailed {
                Text("System figures are unavailable.").foregroundStyle(.secondary)
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 120)
            }
        }
        .task(id: "\(range)|\(state.refreshTick)") { await poll() }
        .alert("Stop stream", isPresented: Binding(get: { stopping != nil }, set: { if !$0 { stopping = nil } }), presenting: stopping) { s in
            TextField("Message to the viewer (optional)", text: $stopMessage)
            Button("Stop stream", role: .destructive) { Task { await kill(s) } }
            Button("Cancel", role: .cancel) {}
        } message: { s in
            Text("Stop \(who(s))? Their player stops and shows your message.")
        }
        .alert("Couldn't stop the stream", isPresented: Binding(get: { stopError != nil }, set: { if !$0 { stopError = nil } })) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(stopError ?? "")
        }
    }

    /// Sessions every three seconds; the load charts every few polls.
    private func poll() async {
        guard let api = state.api else { return }
        do { sys = try await api.system(range: range); sysFailed = false } catch { sysFailed = sys == nil }
        var polls = 0
        while !Task.isCancelled {
            do {
                let a = try await api.activity()
                act = a
                skew = a.serverTime - Date().timeIntervalSince1970
                error = nil
            } catch is CancellationError {
                return
            } catch {
                if act == nil { self.error = statsError(error) }
            }
            try? await Task.sleep(for: .seconds(3))
            polls += 1
            if polls % (range == "1h" ? 4 : 20) == 0, let s = try? await api.system(range: range) { sys = s }
        }
    }

    private func kill(_ s: NowPlaying) async {
        let message = stopMessage.trimmingCharacters(in: .whitespacesAndNewlines)
        stopMessage = ""
        do {
            try await state.api?.killSession(s.sessionId, message: message)
            act?.sessions.removeAll { $0.sessionId == s.sessionId }
            if let a = try? await state.api?.activity() { act = a }
        } catch {
            stopError = error.localizedDescription
        }
    }

    private func who(_ s: NowPlaying) -> String {
        "\(s.title)\(s.subtitle.map { " (\($0))" } ?? "") for \(s.userName)"
    }

    private func totals(_ t: ActivityTotals) -> [KPI] {
        let split = [t.direct > 0 ? "\(t.direct) direct" : nil, t.remux > 0 ? "\(t.remux) direct stream" : nil,
                     t.transcode > 0 ? "\(t.transcode) transcode" : nil].compactMap { $0 }.joined(separator: " · ")
        return [
            KPI(value: Fmt.num(t.streams), label: t.streams == 1 ? "Stream" : "Streams", sub: split.isEmpty ? "Nobody is watching" : split),
            KPI(value: Fmt.rate(t.kbps), label: "Bandwidth",
                sub: t.kbps > 0 ? "\(Fmt.rate(t.lanKbps)) local · \(Fmt.rate(t.wanKbps)) remote" : "Nothing streaming"),
            KPI(value: Fmt.num(t.transcode), label: "Transcodes", sub: t.transcode > 0 ? "Using the server's CPU or GPU" : "None running"),
        ]
    }

    @ViewBuilder
    private func nowPlaying(_ list: [NowPlaying]) -> some View {
        if list.isEmpty {
            StatPanel("Now playing") {
                Text("Nobody is watching right now.").foregroundStyle(.secondary)
            }
        } else {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: compact ? 280 : 360), spacing: 16, alignment: .top)], alignment: .leading, spacing: 16) {
                ForEach(list) { s in
                    NowPlayingCard(session: s, skew: skew) { stopping = s }
                }
            }
        }
    }
}

private struct NowPlayingCard: View {
    @Environment(AppState.self) private var state
    let session: NowPlaying
    let skew: Double
    let stop: () -> Void

    private var n: NowPlaying { session }
    private var method: String { n.audioOnly ? "audio transcode" : n.method }
    private var color: Color { StatColor.method(n.method) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            head
            details
            footer
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(alignment: .top) {
            if let url = state.api?.imageURL(n.backdropPath, size: "w780") {
                RemoteImage(url: url)
                    .frame(height: 150)
                    .frame(maxWidth: .infinity)
                    .clipped()
                    .opacity(0.22)
                    .mask(LinearGradient(colors: [.black, .clear], startPoint: .top, endPoint: .bottom))
                    .allowsHitTesting(false)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .statCard()
    }

    private var head: some View {
        HStack(alignment: .top, spacing: 12) {
            artwork
            VStack(alignment: .leading, spacing: 3) {
                ItemLink(id: n.isLive ? nil : n.itemId) {
                    Text(n.title).font(.headline).lineLimit(2)
                }
                if let sub = n.subtitle, !sub.isEmpty {
                    Text(sub).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                } else if let y = n.year {
                    Text(String(y)).font(.caption).foregroundStyle(.secondary)
                }
                Text("\(Text(n.userName).bold()) \(Text("· \(n.client.isEmpty ? "Unknown player" : n.client)").foregroundStyle(.secondary))")
                    .font(.caption).lineLimit(1).padding(.top, 4)
                if let ip = n.ip, !ip.isEmpty {
                    Text("\(ip) · \(n.local == true ? "Local" : "Remote")").font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                tags.padding(.top, 4)
            }
            Spacer(minLength: 0)
        }
    }

    @ViewBuilder
    private var artwork: some View {
        if n.isLive {
            RemoteImage(url: state.api?.liveLogoURL(n.channelLogo), contentMode: .fit)
                .frame(width: 60, height: 60)
                .padding(6)
                .background(.white.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
        } else {
            RemoteImage(url: state.api?.imageURL(n.posterPath, size: "w185"))
                .frame(width: 60, height: 90)
                .clipShape(RoundedRectangle(cornerRadius: 6))
        }
    }

    private var tags: some View {
        HStack(spacing: 5) {
            Tag(text: Fmt.method(method), color: color)
                .help((n.reasons ?? []).joined(separator: "; "))
            if !n.isLive, let s = n.stream, s.height != nil {
                Tag(text: Fmt.res(s.width, s.height))
            }
            if n.method == "transcode", n.videoCopy != true, let hw = n.hw, !hw.isEmpty {
                Tag(text: Fmt.hw(hw))
            }
            if let lib = n.library, !lib.isEmpty {
                Tag(text: lib, plain: true)
            }
        }
        .lineLimit(1)
    }

    private struct Row {
        var label: String
        var value: Text
        var tip: String?
    }

    private var rows: [Row] {
        var r: [Row] = []
        let dot = Text(Image(systemName: "circle.fill")).font(.system(size: 7)).foregroundStyle(color)
        let engine = n.engine == "mpv" ? Text(" · mpv").foregroundStyle(.secondary) : Text("")
        r.append(Row(label: "Stream", value: Text("\(dot) \(Fmt.method(method))\(engine)"), tip: (n.reasons ?? []).joined(separator: "; ")))
        if !n.isLive {
            let s = n.source ?? TrackInfo(), o = n.stream ?? TrackInfo()
            let from = Fmt.container(s.container), to = Fmt.container(o.container)
            r.append(Row(label: "Container", value: Text(s.container != nil && o.container != nil && from != to ? "\(from) → \(to)" : from)))
            if s.videoCodec != nil { r.append(Row(label: "Video", value: videoLine(s, o))) }
            if s.audioCodec != nil { r.append(Row(label: "Audio", value: audioLine(s, o))) }
            if !n.quality.isEmpty { r.append(Row(label: "Version", value: Text(n.quality))) }
        }
        let rate = n.stream?.kbps ?? n.delivery?.kbps
        let file = n.source?.kbps.flatMap { $0 != rate ? Text(" · file \(Fmt.rate($0))").foregroundStyle(.secondary) : nil } ?? Text("")
        r.append(Row(label: "Bitrate", value: Text("\(Fmt.rate(rate))\(file)")))
        if let d = n.delivery, let bytes = d.bytes, bytes > 0 {
            let now = d.kbps.map { Text(" · \(Fmt.rate($0)) now").foregroundStyle(.secondary) } ?? Text("")
            r.append(Row(label: "Sent", value: Text("\(Fmt.size(bytes))\(now)")))
        }
        if n.method == "transcode", let d = n.delivery {
            var parts: [String] = []
            if let sp = d.speed, sp > 0 { parts.append(String(format: "%.1f× speed", sp)) }
            if d.encodeDone == true { parts.append("Finished encoding") } else if let b = d.bufferSeconds, b > 0 { parts.append("\(Int(b.rounded())) s ahead") }
            if let hw = n.hw, !hw.isEmpty, n.videoCopy != true {
                parts.append(Fmt.hw(hw) + (n.hwDecode == true ? " decode + encode" : " encode"))
            } else if n.videoCopy == true {
                parts.append("Audio only")
            }
            let slow = (d.speed ?? 1) < 1 && (d.speed ?? 0) > 0
            r.append(Row(label: "Transcoder", value: Text(parts.isEmpty ? "Starting" : parts.joined(separator: " · ")).foregroundStyle(slow ? Color.orange : Color.primary),
                         tip: slow ? "Encoding slower than real time: the viewer may buffer." : nil))
        }
        return r
    }

    private func videoLine(_ s: TrackInfo, _ o: TrackInfo) -> Text {
        let fps = s.frameRate.map { "\(String(format: "%g", ($0 * 100).rounded() / 100)) fps" }
        let hdr = s.hdr.flatMap { $0.isEmpty ? nil : $0 } ?? "SDR"
        let src = [Fmt.codec(s.videoCodec) + (s.videoProfile.map { " \($0)" } ?? ""), Fmt.res(s.width, s.height), hdr, fps ?? ""]
            .filter { !$0.isEmpty }.joined(separator: " · ")
        if o.video == "transcode" {
            let out = [Fmt.codec(o.videoCodec), Fmt.res(o.width, o.height), o.kbps.map(Fmt.rate) ?? "", n.tonemap == true ? "tone-mapped to SDR" : ""]
                .filter { !$0.isEmpty }.joined(separator: " · ")
            return Text("\(src)\n\(Text("→").foregroundStyle(.secondary)) \(out)")
        }
        let how = o.video == "copy" ? (n.stripDolbyVision == true ? "Copied, Dolby Vision removed" : "Copied") : "Direct"
        return Text("\(src) \(Text("· \(how)").foregroundStyle(.secondary))")
    }

    private func audioLine(_ s: TrackInfo, _ o: TrackInfo) -> Text {
        let src = [Fmt.codec(s.audioCodec), Fmt.channels(s.audioChannels, s.audioLayout), Fmt.language(s.audioLanguage)]
            .filter { !$0.isEmpty }.joined(separator: " · ")
        if o.audio == "transcode" {
            let out = [Fmt.codec(o.audioCodec), Fmt.channels(o.audioChannels, o.audioLayout)].filter { !$0.isEmpty }.joined(separator: " · ")
            return Text("\(src)\n\(Text("→").foregroundStyle(.secondary)) \(out)")
        }
        return Text("\(src) \(Text("· \(o.audio == "copy" ? "Copied" : "Direct")").foregroundStyle(.secondary))")
    }

    private var details: some View {
        Grid(alignment: .topLeading, horizontalSpacing: 12, verticalSpacing: 5) {
            ForEach(rows, id: \.label) { row in
                GridRow {
                    Text(row.label).foregroundStyle(.secondary)
                    row.value.fixedSize(horizontal: false, vertical: true)
                        .help(row.tip ?? "")
                }
            }
        }
        .font(.caption)
    }

    /// Moves on every second from the last report, like the web dashboard.
    private var footer: some View {
        TimelineView(.periodic(from: .now, by: 1)) { ctx in
            let pos = n.position(at: ctx.date.timeIntervalSince1970 + skew)
            let fraction = n.isLive ? 1 : n.duration > 0 ? min(1, pos / n.duration) : 0
            VStack(spacing: 8) {
                GeometryReader { g in
                    ZStack(alignment: .leading) {
                        Capsule().fill(.white.opacity(0.1))
                        Capsule().fill(color).frame(width: g.size.width * fraction)
                    }
                }
                .frame(height: 4)
                HStack(spacing: 8) {
                    Label(n.isPaused ? "Paused" : "Playing", systemImage: n.isPaused ? "pause.fill" : "play.fill")
                        .labelStyle(.titleAndIcon)
                        .foregroundStyle(n.isPaused ? Color.secondary : Color.primary)
                    if n.isLive {
                        Text("Watching for \(formatClock(pos))").monospacedDigit()
                    } else {
                        Text("\(formatClock(pos)) / \(formatClock(n.duration))").monospacedDigit()
                        if n.duration > 0, !n.isPaused {
                            Text("ends \(ctx.date.addingTimeInterval(n.duration - pos).formatted(date: .omitted, time: .shortened))")
                                .foregroundStyle(.secondary).lineLimit(1)
                        }
                    }
                    Spacer(minLength: 0)
                    Button("Stop", role: .destructive, action: stop)
                        .controlSize(.small)
                }
                .font(.caption)
            }
        }
    }
}

private struct SystemSection: View {
    let sys: SystemResponse

    private var info: SystemInfo? { sys.info }
    private var samples: [SystemSample] { sys.samples ?? [] }
    private var proc: String { info?.procLabel ?? "Wren TV" }
    private var hostName: String { info?.host == false ? "Machine" : "Host" }
    private var memCap: Int64? {
        guard let limit = info?.memLimit, let total = info?.memTotal, limit > 0, limit < total else { return nil }
        return limit
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            KPIRow(items: kpis)
            PanelGrid(minimum: 360) {
                StatPanel("CPU") { chart(cpu: true) }
                StatPanel("Memory") { chart(cpu: false) }
                StatPanel("Server") { facts }
                StatPanel("Storage") { disks }
            }
        }
    }

    private var kpis: [KPI] {
        guard let last = samples.last else { return [KPI(value: "—", label: "CPU"), KPI(value: "—", label: "Memory used")] }
        return [
            KPI(value: "\(Int(last.cpu.rounded()))%", label: "CPU",
                sub: info?.procCpuAvailable == false ? "" : "\(proc) \(String(format: "%.1f", last.procCpu))%"),
            KPI(value: Fmt.size(last.mem), label: "Memory used",
                sub: "\(proc) \(Fmt.size(last.procMem, zero: "0 B")) of \(Fmt.size(info?.memTotal))"),
        ]
    }

    private func lines(cpu: Bool) -> [LineSeries] {
        var series = [LineSeries(name: hostName, color: StatColor.a, values: samples.map { cpu ? $0.cpu : Double($0.mem) })]
        if !cpu || info?.procCpuAvailable != false {
            series.append(LineSeries(name: proc, color: StatColor.b, values: samples.map { cpu ? $0.procCpu : Double($0.procMem) }))
        }
        return series
    }

    @ViewBuilder
    private func chart(cpu: Bool) -> some View {
        let times = samples.map { Date(timeIntervalSince1970: TimeInterval($0.t)) }
        let series = lines(cpu: cpu)
        Legend(items: series.map { ($0.name, $0.color) })
        if samples.count > 1 {
            if cpu {
                LineChart(times: times, series: series, yMax: 100,
                          format: { String(format: $0 < 10 ? "%.1f%%" : "%.0f%%", $0) }, axis: { "\(Int($0.rounded()))%" })
            } else {
                LineChart(times: times, series: series, yMax: info.map { Double($0.memTotal) },
                          format: { Fmt.size(Int64($0), zero: "0 B") }, axis: { Fmt.gigabytes($0) })
            }
        } else {
            Text("Collecting samples…").foregroundStyle(.secondary).frame(maxWidth: .infinity, minHeight: 120)
        }
    }

    private var facts: some View {
        let hw = sys.hardware
        let rows: [(String, String)] = [
            ("Wren TV", sys.version ?? "—"),
            ("System", [Fmt.os(info?.os), info?.arch ?? ""].filter { !$0.isEmpty }.joined(separator: " · ")),
            ("Processor", info.map { i in
                "\(i.cpus) core\(i.cpus == 1 ? "" : "s")" + (i.cpuLimit.map { " · \(proc.lowercased()) limited to \(String(format: "%g", $0))" } ?? "")
            } ?? "—"),
            ("Memory", (info.map { Fmt.size($0.memTotal) } ?? "—") + (memCap.map { " · \(proc.lowercased()) limited to \(Fmt.size($0))" } ?? "")),
            ("Transcoding", hw.map { "\(Fmt.hw($0.kind)) · \($0.h264Encoder ?? "")\($0.hwDecode == true ? " · hardware decode" : "")" } ?? "—"),
            ("Tone mapping", hw?.tonemap.flatMap { $0.isEmpty || $0 == "none" ? nil : $0 + (hw?.dovi == true ? " · Dolby Vision aware" : "") } ?? "Not available"),
            ("Up for", sys.uptime.map(Fmt.uptime) ?? "—"),
        ]
        return Grid(alignment: .topLeading, horizontalSpacing: 14, verticalSpacing: 7) {
            ForEach(rows, id: \.0) { k, v in
                GridRow {
                    Text(k).foregroundStyle(.secondary)
                    Text(v).textSelection(.enabled)
                }
            }
        }
        .font(.callout)
    }

    @ViewBuilder
    private var disks: some View {
        let list = sys.disks ?? []
        if list.isEmpty {
            Text("No disks found.").foregroundStyle(.secondary)
        } else {
            VStack(alignment: .leading, spacing: 14) {
                ForEach(list, id: \.path) { d in
                    let used = d.total - d.free
                    let pct = d.total > 0 ? Double(used) / Double(d.total) : 0
                    VStack(alignment: .leading, spacing: 5) {
                        HStack {
                            Text(d.label).fontWeight(.semibold).lineLimit(1)
                            Spacer()
                            Text("\(Fmt.size(d.free)) free of \(Fmt.size(d.total))").monospacedDigit().foregroundStyle(.secondary)
                        }
                        Meter(fraction: pct, color: pct > 0.9 ? StatColor.transcode : StatColor.a)
                            .accessibilityLabel("\(d.label) used")
                            .accessibilityValue("\(Int((pct * 100).rounded())) percent")
                        Text("\(d.path) · \(Int((pct * 100).rounded()))% used").font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                    .font(.callout)
                }
            }
        }
    }
}

// MARK: - Overview

/// Loads the period's stats once for Overview and Graphs.
private struct StatsLoader<Content: View>: View {
    @Environment(AppState.self) private var state
    @Environment(StatsNav.self) private var nav
    let days: Int
    @ViewBuilder var content: (StatsResponse) -> Content
    @State private var error: String?

    var body: some View {
        Group {
            if let d = nav.stats[days] {
                content(d)
            } else if let error {
                LoadFailed(message: error)
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 200)
            }
        }
        .task(id: "\(days)|\(state.refreshTick)") {
            do {
                if let d = try await state.api?.stats(days: days) { nav.stats[days] = d }
                error = nil
            } catch {
                if nav.stats[days] == nil { self.error = statsError(error) }
            }
        }
    }
}

private struct OverviewTab: View {
    @Environment(StatsNav.self) private var nav
    let days: Int

    var body: some View {
        StatsLoader(days: days) { d in
            let s = d.stats
            VStack(alignment: .leading, spacing: 18) {
                KPIRow(items: kpis(s))
                PanelGrid(minimum: 320) {
                    StatPanel("Most watched movies") { TopList(rows: s.topMovies ?? []) }
                    StatPanel("Most popular movies") { TopList(rows: s.popularMovies ?? [], byUsers: true) }
                    StatPanel("Most watched shows") { TopList(rows: s.topShows ?? []) }
                    StatPanel("Most popular shows") { TopList(rows: s.popularShows ?? [], byUsers: true) }
                    StatPanel("Top viewers") { TopUsers(rows: s.topUsers ?? []) }
                    StatPanel("Top players") {
                        HBars(rows: s.byClient ?? [])
                        Text("Libraries").font(.headline).padding(.top, 10)
                        HBars(rows: (s.byLibrary ?? []).filter { $0.plays > 0 }.map { StatCount(name: $0.name, count: $0.plays) })
                    }
                }
                if let added = d.recentlyAdded, !added.isEmpty {
                    StatPanel("Recently added") { RecentlyAddedRow(items: added) }
                }
                StatPanel("Recent plays", accessory: {
                    Button("Full history") { nav.tab = .history }.buttonStyle(.borderless).font(.callout)
                }) {
                    HistoryTable(rows: Array((d.recent ?? []).prefix(15)))
                }
            }
        }
    }

    private func kpis(_ s: PlayStats) -> [KPI] {
        let transcodes = (s.byMethod ?? []).filter { $0.name == "transcode" || $0.name == "audio transcode" }.reduce(0) { $0 + $1.count }
        return [
            KPI(value: Fmt.num(s.plays), label: "Plays"),
            KPI(value: Fmt.hours(s.hours), label: "Hours watched",
                sub: s.hours > 0 ? "\(Fmt.hours(s.movieHours ?? 0)) h movies · \(Fmt.hours(s.episodeHours ?? 0)) h TV" : ""),
            KPI(value: Fmt.num(s.users), label: "Active viewers"),
            KPI(value: Fmt.num(s.titles), label: "Titles watched"),
            KPI(value: Fmt.num(s.peakStreams ?? 0), label: "Most at once",
                sub: s.peakAt.map { Date(timeIntervalSince1970: TimeInterval($0)).formatted(.dateTime.month(.abbreviated).day().hour().minute()) } ?? ""),
            KPI(value: s.plays > 0 ? "\(Int((Double(transcodes) / Double(s.plays) * 100).rounded()))%" : "—", label: "Transcoded",
                sub: s.plays > 0 ? "\(Fmt.num(transcodes)) of \(Fmt.num(s.plays)) plays" : ""),
        ]
    }
}

private struct RecentlyAddedRow: View {
    @Environment(AppState.self) private var state
    let items: [RecentlyAdded]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            LazyHStack(alignment: .top, spacing: 12) {
                ForEach(items, id: \.itemId) { a in
                    let episode = a.kind == "episode"
                    let title = episode ? (a.showTitle ?? a.title) : a.title
                    let sub = episode ? "S\(a.season ?? 0) · E\(a.episode ?? 0)" : a.year.map(String.init) ?? ""
                    ItemLink(id: episode ? (a.showId ?? a.itemId) : a.itemId) {
                        VStack(alignment: .leading, spacing: 4) {
                            RemoteImage(url: state.api?.imageURL(a.posterPath, size: "w185"))
                                .frame(width: 100, height: 150)
                                .clipShape(RoundedRectangle(cornerRadius: 6))
                            Text(title).font(.caption.weight(.semibold)).lineLimit(1)
                            Text([sub, Fmt.ago(a.addedAt)].filter { !$0.isEmpty }.joined(separator: " · "))
                                .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                        }
                        .frame(width: 100, alignment: .leading)
                    }
                    .help(episode ? "\(title) — \(a.title)" : title)
                }
            }
        }
    }
}

// MARK: - Graphs

private struct GraphsTab: View {
    let days: Int

    var body: some View {
        StatsLoader(days: days) { d in
            GraphsBody(s: d.stats, days: days)
        }
    }
}

private struct GraphsBody: View {
    let s: PlayStats
    let days: Int

    private var every: Int { days <= 7 ? 1 : days <= 30 ? 5 : days <= 90 ? 14 : 60 }
    private var daily: [DayStat] { s.daily }
    private func other(_ x: DayStat) -> Int { max(0, x.plays - (x.movies ?? 0) - (x.episodes ?? 0)) }
    private var hasOther: Bool { daily.contains { other($0) > 0 } }
    private var typeSeries: [StackSeries] {
        var t = [StackSeries(name: "Movies", color: StatColor.a), StackSeries(name: "TV", color: StatColor.b)]
        if hasOther { t.append(StackSeries(name: "Other", color: StatColor.c)) }
        return t
    }
    private let methods = [StackSeries(name: "Direct play", color: StatColor.direct), StackSeries(name: "Direct stream", color: StatColor.remux),
                           StackSeries(name: "Transcode", color: StatColor.transcode)]
    private let plays = [StackSeries(name: "Plays", color: StatColor.a)]

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            StatPanel("Plays per day by type") {
                if s.plays > 0 {
                    Legend(items: typeSeries.map { ($0.name, $0.color) })
                    StackedBars(points: daily.map { x in
                        StackPoint(id: x.date, label: Fmt.shortDay(x.date), title: Fmt.longDay(x.date),
                                   values: hasOther ? [x.movies ?? 0, x.episodes ?? 0, other(x)] : [x.movies ?? 0, x.episodes ?? 0],
                                   note: "\(Fmt.hours(x.hours)) h watched")
                    }, series: typeSeries, labelEvery: every, height: 190)
                } else { EmptyChart() }
            }
            StatPanel("Plays per day by stream type") {
                if s.plays > 0 {
                    Legend(items: methods.map { ($0.name, $0.color) })
                    StackedBars(points: daily.map { x in
                        StackPoint(id: x.date, label: Fmt.shortDay(x.date), title: Fmt.longDay(x.date), values: [x.direct ?? 0, x.remux ?? 0, x.transcode ?? 0])
                    }, series: methods, labelEvery: every, height: 190)
                } else { EmptyChart() }
            }
            PanelGrid(minimum: 360) {
                StatPanel("Most streams at once, per day") {
                    if s.plays > 0 {
                        StackedBars(points: daily.map { x in
                            StackPoint(id: x.date, label: Fmt.shortDay(x.date), title: Fmt.longDay(x.date), values: [x.peak ?? 0])
                        }, series: [StackSeries(name: "At once", color: StatColor.a)], labelEvery: every, height: 150)
                    } else { EmptyChart() }
                }
                StatPanel("Plays per month") {
                    let monthly = s.monthly ?? []
                    if monthly.contains(where: { $0.plays > 0 }) {
                        Legend(items: typeSeries.prefix(2).map { ($0.name, $0.color) })
                        StackedBars(points: monthly.map { m in
                            StackPoint(id: m.month, label: Fmt.month(m.month), title: Fmt.month(m.month, long: true),
                                       values: [m.movies, m.episodes], note: "\(Fmt.hours(m.hours)) h watched")
                        }, series: Array(typeSeries.prefix(2)), labelEvery: 2, height: 150)
                    } else { EmptyChart() }
                }
                StatPanel("By time of day") {
                    if s.plays > 0 {
                        StackedBars(points: s.byHour.enumerated().map { h, v in
                            StackPoint(id: String(h), label: Fmt.hour(h), title: "\(Fmt.hour(h))–\(Fmt.hour((h + 1) % 24))", values: [v])
                        }, series: plays, labelEvery: 6, height: 140)
                    } else { EmptyChart() }
                }
                StatPanel("By day of week") {
                    if s.plays > 0 {
                        StackedBars(points: s.byWeekday.enumerated().map { i, v in
                            StackPoint(id: String(i), label: Fmt.weekday(i), title: Fmt.weekday(i, long: true), values: [v])
                        }, series: plays, height: 140)
                    } else { EmptyChart() }
                }
                StatPanel("Source resolution") { HBars(rows: s.sourceResolutions ?? []) }
                StatPanel("Stream resolution") { HBars(rows: s.streamResolutions ?? []) }
                StatPanel("Players by stream type") {
                    Legend(items: methods.map { ($0.name, $0.color) })
                    SplitBars(rows: s.clientMethods ?? [])
                }
                StatPanel("Viewers by stream type") {
                    Legend(items: methods.map { ($0.name, $0.color) })
                    SplitBars(rows: s.userMethods ?? [])
                }
            }
        }
    }
}

// MARK: - History

private struct HistoryTab: View {
    @Environment(AppState.self) private var state
    @Environment(StatsNav.self) private var nav
    @Environment(\.statsCompact) private var compact
    @State private var search = ""
    @State private var days = 0
    @State private var kind = ""
    @State private var method = ""
    @State private var offset = 0
    @State private var page: HistoryPage?
    @State private var viewers: [Viewer] = []
    @State private var error: String?
    private let limit = 50

    var body: some View {
        @Bindable var nav = nav
        StatPanel("History") {
            filters(user: $nav.historyUser)
            if let page {
                if page.rows.isEmpty {
                    Text("No plays match.").foregroundStyle(.secondary).padding(.vertical, 20)
                } else {
                    HistoryTable(rows: page.rows, full: true)
                }
                pager(page)
            } else if let error {
                Text(error).foregroundStyle(.red)
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 120)
            }
        }
        .task { viewers = (try? await state.api?.viewers()) ?? [] }
        .task(id: "\(search)|\(days)|\(nav.historyUser)|\(kind)|\(method)|\(offset)|\(state.refreshTick)") {
            // Waits out typing in the search box.
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled, let api = state.api else { return }
            do {
                page = try await api.history(days: days, user: nav.historyUser, kind: kind, method: method, search: search, offset: offset, limit: limit)
                error = nil
            } catch is CancellationError {
            } catch {
                self.error = statsError(error)
            }
        }
        .onChange(of: "\(search)|\(days)|\(nav.historyUser)|\(kind)|\(method)") { offset = 0 }
    }

    @ViewBuilder
    private func filters(user: Binding<String>) -> some View {
        let field = TextField("Search titles, players or addresses", text: $search)
            .textFieldStyle(.plain)
            .padding(.horizontal, 10)
            .frame(height: 30)
            .background(.white.opacity(0.07), in: RoundedRectangle(cornerRadius: 7))
        let menus = HStack(spacing: 8) {
            Picker("Period", selection: $days) {
                Text("All time").tag(0)
                Text("Last 24 hours").tag(1)
                Text("Last 7 days").tag(7)
                Text("Last 30 days").tag(30)
                Text("Last 90 days").tag(90)
                Text("Last year").tag(365)
            }
            Picker("Viewer", selection: user) {
                Text("All viewers").tag("")
                ForEach(viewers) { v in Text(v.name).tag(v.userId) }
            }
            Picker("Type", selection: $kind) {
                Text("Everything").tag("")
                Text("Movies").tag("movie")
                Text("TV episodes").tag("episode")
            }
            Picker("Stream type", selection: $method) {
                Text("Any playback").tag("")
                Text("Direct play").tag("direct")
                Text("Direct stream").tag("remux")
                Text("Transcode").tag("transcode")
            }
        }
        .pickerStyle(.menu)
        .labelsHidden()
        .fixedSize()
        if compact {
            field
            ScrollView(.horizontal, showsIndicators: false) { menus }
        } else {
            HStack(spacing: 10) {
                field.frame(minWidth: 200)
                menus
            }
        }
    }

    private func pager(_ page: HistoryPage) -> some View {
        let end = min(page.total, offset + page.rows.count)
        return HStack {
            if page.total > 0 {
                Text("\(Fmt.num(offset + 1))–\(Fmt.num(end)) of \(Fmt.num(page.total))").font(.callout).foregroundStyle(.secondary).monospacedDigit()
            }
            Spacer()
            Button("Previous") { offset = max(0, offset - limit) }.disabled(offset == 0)
            Button("Next") { offset += limit }.disabled(end >= page.total)
        }
        .controlSize(.small)
        .padding(.top, 6)
    }
}

/// Plays as rows: a table when there's room, stacked lines on a phone.
private struct HistoryTable: View {
    @Environment(AppState.self) private var state
    @Environment(StatsNav.self) private var nav
    @Environment(\.statsCompact) private var compact
    let rows: [HistoryRow]
    var full = false
    var noViewer = false

    var body: some View {
        if rows.isEmpty {
            Text("Plays show up here as people watch.").foregroundStyle(.secondary)
        } else if compact {
            VStack(spacing: 0) {
                ForEach(rows) { h in
                    compactRow(h).padding(.vertical, 8)
                    if h.id != rows.last?.id { Divider().opacity(0.5) }
                }
            }
        } else {
            Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 8) {
                GridRow {
                    Text("Title")
                    if !noViewer { Text("Viewer") }
                    Text("Player")
                    if full { Text("Address") }
                    Text("Playback")
                    Text("Watched").gridColumnAlignment(.trailing)
                    if full { Text("Progress").gridColumnAlignment(.trailing) }
                    Text("When")
                }
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                ForEach(rows) { h in
                    Divider().opacity(0.5)
                    GridRow {
                        title(h).frame(minWidth: 200, maxWidth: .infinity, alignment: .leading)
                        if !noViewer { viewer(h) }
                        Text(h.client.isEmpty ? "—" : h.client).font(.caption).lineLimit(1)
                        if full { Text(h.ip ?? "—").font(.caption.monospacedDigit()).lineLimit(1) }
                        playback(h)
                        Text(watched(h)).monospacedDigit()
                        if full { Text(progress(h)).monospacedDigit() }
                        when(h)
                    }
                    .font(.callout)
                }
            }
        }
    }

    private func title(_ h: HistoryRow) -> some View {
        HStack(spacing: 10) {
            RemoteImage(url: state.api?.imageURL(h.posterPath, size: "w92"))
                .frame(width: 30, height: 45)
                .clipShape(RoundedRectangle(cornerRadius: 3))
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 4) {
                    ItemLink(id: h.itemId) { Text(titleText(h)).fontWeight(.semibold).lineLimit(1) }
                    if h.completed {
                        Image(systemName: "checkmark.circle.fill").font(.caption).foregroundStyle(StatColor.direct).help("Finished")
                    }
                }
                if let q = h.quality, !q.isEmpty { Text(q).font(.caption).foregroundStyle(.secondary).lineLimit(1) }
            }
        }
    }

    private func viewer(_ h: HistoryRow) -> some View {
        Button(h.userName) { if let id = h.userId { nav.openViewer(id) } }
            .buttonStyle(.plain)
            .lineLimit(1)
    }

    private func playback(_ h: HistoryRow) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 5) {
                Circle().fill(StatColor.method(h.method)).frame(width: 7, height: 7)
                Text(Fmt.method(methodName(h)))
            }
            if let res = resolution(h) { Text(res).foregroundStyle(.secondary) }
        }
        .font(.caption)
        .lineLimit(1)
        .help(h.reasons ?? "")
    }

    private func when(_ h: HistoryRow) -> some View {
        let date = Date(timeIntervalSince1970: TimeInterval(h.startedAt))
        return Text(full ? date.formatted(.dateTime.month(.abbreviated).day().hour().minute()) : Fmt.ago(h.startedAt))
            .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            .help(date.formatted(date: .complete, time: .shortened))
    }

    private func compactRow(_ h: HistoryRow) -> some View {
        HStack(alignment: .top, spacing: 10) {
            RemoteImage(url: state.api?.imageURL(h.posterPath, size: "w92"))
                .frame(width: 36, height: 54)
                .clipShape(RoundedRectangle(cornerRadius: 4))
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 4) {
                    ItemLink(id: h.itemId) { Text(titleText(h)).fontWeight(.semibold).lineLimit(1) }
                    if h.completed { Image(systemName: "checkmark.circle.fill").font(.caption).foregroundStyle(StatColor.direct) }
                }
                Text([noViewer ? nil : h.userName, h.client.isEmpty ? nil : h.client].compactMap { $0 }.joined(separator: " · "))
                    .font(.caption).lineLimit(1)
                HStack(spacing: 5) {
                    Circle().fill(StatColor.method(h.method)).frame(width: 7, height: 7)
                    Text([Fmt.method(methodName(h)), resolution(h), watched(h), Fmt.ago(h.startedAt)].compactMap { $0 }.joined(separator: " · "))
                        .lineLimit(1)
                }
                .font(.caption)
                .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .contentShape(Rectangle())
    }

    private func titleText(_ h: HistoryRow) -> String {
        h.kind == "episode" && h.showTitle != nil ? "\(h.showTitle!) — \(h.title)" : h.title
    }
    private func methodName(_ h: HistoryRow) -> String { h.method == "transcode" && h.videoCopy == true ? "audio transcode" : h.method }
    private func resolution(_ h: HistoryRow) -> String? {
        guard let height = h.height, height > 0 else { return nil }
        var r = Fmt.res(0, height)
        if let sh = h.streamHeight, sh > 0, sh != height { r += " → \(Fmt.res(0, sh))" }
        return r
    }
    private func watched(_ h: HistoryRow) -> String { h.playedSeconds >= 60 ? formatDuration(h.playedSeconds) : "<1 min" }
    private func progress(_ h: HistoryRow) -> String {
        guard let d = h.duration, d > 0 else { return "—" }
        return "\(min(100, Int(((h.position ?? 0) / d * 100).rounded())))%"
    }
}

// MARK: - Viewers

private struct ViewersTab: View {
    @Environment(AppState.self) private var state
    @Environment(StatsNav.self) private var nav
    @Environment(\.statsCompact) private var compact
    @State private var list: [Viewer]?
    @State private var error: String?

    var body: some View {
        if let id = nav.viewer {
            ViewerDetailView(id: id)
        } else {
            StatPanel("Viewers") {
                if let list {
                    if list.isEmpty {
                        Text("Nobody has an account yet.").foregroundStyle(.secondary)
                    } else if compact {
                        VStack(spacing: 0) {
                            ForEach(list) { v in
                                Button { nav.openViewer(v.userId) } label: { compactRow(v) }.buttonStyle(.plain).padding(.vertical, 8)
                                if v.id != list.last?.id { Divider().opacity(0.5) }
                            }
                        }
                    } else {
                        table(list)
                    }
                } else if let error {
                    Text(error).foregroundStyle(.red)
                } else {
                    ProgressView().frame(maxWidth: .infinity, minHeight: 120)
                }
            }
            .task(id: state.refreshTick) {
                do { list = try await state.api?.viewers(); error = nil } catch { self.error = statsError(error) }
            }
        }
    }

    private func table(_ list: [Viewer]) -> some View {
        Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 8) {
            GridRow {
                Text("Viewer")
                Text("Plays").gridColumnAlignment(.trailing)
                Text("Hours").gridColumnAlignment(.trailing)
                Text("Last watched")
                Text("Last player")
                Text("Address")
                Text("Last seen")
            }
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            ForEach(list) { v in
                Divider().opacity(0.5)
                GridRow {
                    Button { nav.openViewer(v.userId) } label: {
                        HStack(spacing: 8) {
                            Avatar(name: v.name, size: 26)
                            Text(v.name).fontWeight(.semibold).lineLimit(1)
                            if v.admin { Tag(text: "Admin", plain: true) }
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    Text(Fmt.num(v.plays)).monospacedDigit()
                    Text(Fmt.hours(v.hours)).monospacedDigit()
                    Text(v.lastPlayed ?? "—").lineLimit(1).frame(maxWidth: 240, alignment: .leading)
                    Text(v.lastClient ?? "—").font(.caption).lineLimit(1)
                    Text(v.lastIp ?? "—").font(.caption.monospacedDigit()).lineLimit(1)
                    Text(v.lastSeen.map(Fmt.ago) ?? "never").font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                .font(.callout)
            }
        }
    }

    private func compactRow(_ v: Viewer) -> some View {
        HStack(spacing: 10) {
            Avatar(name: v.name, size: 34)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(v.name).fontWeight(.semibold)
                    if v.admin { Tag(text: "Admin", plain: true) }
                }
                Text(v.lastPlayed ?? "Nothing watched yet").font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 2) {
                Text("\(Fmt.num(v.plays)) plays").font(.callout.monospacedDigit())
                Text(v.lastSeen.map(Fmt.ago) ?? "never").font(.caption).foregroundStyle(.secondary)
            }
        }
        .contentShape(Rectangle())
    }
}

private struct ViewerDetailView: View {
    @Environment(AppState.self) private var state
    @Environment(StatsNav.self) private var nav
    let id: String
    @State private var v: ViewerDetail?
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Button { nav.viewer = nil } label: { Label("All viewers", systemImage: "chevron.left") }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
            if let v {
                header(v)
                KPIRow(items: v.totals.map { KPI(value: Fmt.num($0.plays), label: $0.label, sub: "\(Fmt.hours($0.hours)) h") })
                PanelGrid(minimum: 360) {
                    StatPanel("Most watched") { TopList(rows: v.topTitles, own: true) }
                    StatPanel("Stream types") {
                        HBars(rows: [StatCount(name: "Direct play", count: v.methods.direct), StatCount(name: "Direct stream", count: v.methods.remux),
                                     StatCount(name: "Transcode", count: v.methods.transcode)].filter { $0.count > 0 })
                        Text("Players").font(.headline).padding(.top, 10)
                        UsedTable(rows: v.players, addresses: false)
                    }
                }
                StatPanel("Addresses") { UsedTable(rows: v.addresses, addresses: true) }
                StatPanel("Recent plays", accessory: {
                    Button("Full history") {
                        nav.historyUser = v.userId
                        nav.tab = .history
                    }
                    .buttonStyle(.borderless).font(.callout)
                }) {
                    HistoryTable(rows: v.recent, full: true, noViewer: true)
                }
            } else if let error {
                LoadFailed(message: error)
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 200)
            }
        }
        .task(id: "\(id)|\(state.refreshTick)") {
            do { v = try await state.api?.viewer(id); error = nil } catch { self.error = statsError(error) }
        }
    }

    private func header(_ v: ViewerDetail) -> some View {
        HStack(spacing: 14) {
            Avatar(name: v.name, size: 54)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text(v.name).font(.title2.bold())
                    if v.admin { Tag(text: "Admin", plain: true) }
                }
                Text([v.joined > 0 ? "Joined \(Date(timeIntervalSince1970: TimeInterval(v.joined)).formatted(date: .abbreviated, time: .omitted))" : nil,
                      v.lastSeen.map { "Last seen \(Fmt.ago($0))" }].compactMap { $0 }.joined(separator: " · "))
                    .font(.callout).foregroundStyle(.secondary)
            }
        }
    }
}

/// The players or addresses a viewer has watched from.
private struct UsedTable: View {
    let rows: [ViewerDetail.UsedFrom]
    let addresses: Bool

    var body: some View {
        if rows.isEmpty {
            Text("Nothing yet.").foregroundStyle(.secondary)
        } else {
            Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 7) {
                GridRow {
                    Text(addresses ? "Address" : "Player")
                    if addresses { Text("Player") }
                    Text("Plays").gridColumnAlignment(.trailing)
                    Text("Hours").gridColumnAlignment(.trailing)
                    Text("Last used")
                }
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                ForEach(rows, id: \.name) { u in
                    GridRow {
                        HStack(spacing: 6) {
                            Text(u.name).fontWeight(.semibold).lineLimit(1).monospacedDigit()
                            if addresses { Text(Fmt.isPrivate(u.name) ? "Local" : "Remote").font(.caption).foregroundStyle(.secondary) }
                        }
                        if addresses { Text(u.detail ?? "—").font(.caption).lineLimit(1) }
                        Text(Fmt.num(u.plays)).monospacedDigit()
                        Text(Fmt.hours(u.hours)).monospacedDigit()
                        Text(Fmt.ago(u.lastSeen)).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                    .font(.callout)
                }
            }
        }
    }
}

// MARK: - Libraries

private struct LibrariesTab: View {
    @Environment(AppState.self) private var state
    @Environment(\.statsCompact) private var compact
    @State private var lib: LibraryStats?
    @State private var error: String?

    var body: some View {
        Group {
            if let lib {
                VStack(alignment: .leading, spacing: 18) {
                    KPIRow(items: [
                        KPI(value: Fmt.num(lib.movies), label: "Movies"),
                        KPI(value: Fmt.num(lib.shows), label: "Shows"),
                        KPI(value: Fmt.num(lib.episodes), label: "Episodes"),
                        KPI(value: Fmt.size(lib.bytes, zero: "0 B"), label: "On disk", sub: "\(Fmt.num(lib.files)) files"),
                        KPI(value: Fmt.hours(lib.hours), label: "Hours of media"),
                    ])
                    if let libs = lib.libraries, !libs.isEmpty {
                        StatPanel("Libraries") { libraries(libs) }
                    }
                    PanelGrid(minimum: 360) {
                        StatPanel("Resolution") {
                            HBars(rows: lib.resolutions ?? [])
                            Text("Dynamic range").font(.headline).padding(.top, 10)
                            HBars(rows: lib.hdr ?? [])
                        }
                        StatPanel("Video codec") {
                            HBars(rows: lib.videoCodecs ?? [])
                            Text("Container").font(.headline).padding(.top, 10)
                            HBars(rows: lib.containers ?? [])
                        }
                    }
                }
            } else if let error {
                LoadFailed(message: error)
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 200)
            }
        }
        .task(id: state.refreshTick) {
            do { lib = try await state.api?.stats(days: 30).library; error = nil } catch { if lib == nil { self.error = statsError(error) } }
        }
    }

    @ViewBuilder
    private func libraries(_ libs: [LibraryStats.Lib]) -> some View {
        if compact {
            VStack(spacing: 0) {
                ForEach(libs, id: \.id) { l in
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(l.name).fontWeight(.semibold)
                            Text(libSummary(l)).font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        VStack(alignment: .trailing, spacing: 2) {
                            Text("\(Fmt.num(l.plays ?? 0)) plays").font(.callout.monospacedDigit())
                            Text("\(Fmt.hours(l.hours ?? 0)) h").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                    .padding(.vertical, 8)
                    if l.id != libs.last?.id { Divider().opacity(0.5) }
                }
            }
        } else {
            Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 8) {
                GridRow {
                    Text("Name")
                    Text("Type")
                    Text("Titles").gridColumnAlignment(.trailing)
                    Text("Episodes").gridColumnAlignment(.trailing)
                    Text("Size").gridColumnAlignment(.trailing)
                    Text("Plays").gridColumnAlignment(.trailing)
                    Text("Hours watched").gridColumnAlignment(.trailing)
                    Text("Last played")
                }
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                ForEach(libs, id: \.id) { l in
                    Divider().opacity(0.5)
                    GridRow {
                        Text(l.name).fontWeight(.semibold).lineLimit(1)
                        Text(l.type == "shows" ? "Shows" : "Movies").foregroundStyle(.secondary)
                        Text(Fmt.num(l.items)).monospacedDigit()
                        Text(l.type == "shows" ? Fmt.num(l.episodes ?? 0) : "—").monospacedDigit()
                        Text(l.bytes > 0 ? Fmt.size(l.bytes) : "—").monospacedDigit()
                        Text(Fmt.num(l.plays ?? 0)).monospacedDigit()
                        Text(Fmt.hours(l.hours ?? 0)).monospacedDigit()
                        VStack(alignment: .leading, spacing: 1) {
                            Text(l.lastPlayed ?? "—").lineLimit(1)
                            if let at = l.lastPlayedAt { Text(Fmt.ago(at)).font(.caption).foregroundStyle(.secondary) }
                        }
                        .frame(maxWidth: 240, alignment: .leading)
                    }
                    .font(.callout)
                }
            }
        }
    }

    private func libSummary(_ l: LibraryStats.Lib) -> String {
        var parts = ["\(Fmt.num(l.items)) \(l.type == "shows" ? "shows" : "movies")"]
        if l.type == "shows", let e = l.episodes { parts.append("\(Fmt.num(e)) episodes") }
        if l.bytes > 0 { parts.append(Fmt.size(l.bytes)) }
        return parts.joined(separator: " · ")
    }
}

// MARK: - Pieces

private struct KPI: Hashable {
    var value: String
    var label: String
    var sub: String = ""
}

private struct KPIRow: View {
    @Environment(\.statsCompact) private var compact
    let items: [KPI]

    var body: some View {
        // A phone wraps the tiles; wider windows give each one an equal share.
        let columns = compact ? [GridItem(.adaptive(minimum: 140), spacing: 14)] : Array(repeating: GridItem(.flexible(), spacing: 14), count: max(items.count, 1))
        LazyVGrid(columns: columns, spacing: 14) {
            ForEach(items, id: \.label) { k in
                VStack(alignment: .leading, spacing: 4) {
                    Text(k.value).font(.system(size: compact ? 24 : 28, weight: .bold).monospacedDigit()).lineLimit(1).minimumScaleFactor(0.6)
                    Text(k.label.uppercased()).font(.caption.weight(.semibold)).kerning(0.6).foregroundStyle(.secondary).lineLimit(1)
                    Text(k.sub.isEmpty ? " " : k.sub).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(14)
                .statCard(cornerRadius: 10)
            }
        }
    }
}

/// A titled card.
private struct StatPanel<Accessory: View, Content: View>: View {
    let title: String
    @ViewBuilder var accessory: Accessory
    @ViewBuilder var content: Content

    init(_ title: String, @ViewBuilder accessory: () -> Accessory, @ViewBuilder content: () -> Content) {
        self.title = title
        self.accessory = accessory()
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(title).font(.headline)
                Spacer(minLength: 8)
                accessory
            }
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .statCard()
    }
}

private extension StatPanel where Accessory == EmptyView {
    init(_ title: String, @ViewBuilder content: () -> Content) {
        self.init(title, accessory: { EmptyView() }, content: content)
    }
}

/// Panels side by side when there's room.
private struct PanelGrid<Content: View>: View {
    var minimum: CGFloat = 340
    @ViewBuilder var content: Content

    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: minimum), spacing: 18, alignment: .top)], alignment: .leading, spacing: 18) {
            content
        }
    }
}

private struct LoadFailed: View {
    let message: String

    var body: some View {
        ContentUnavailableView("Couldn't load stats", systemImage: "exclamationmark.triangle", description: Text(message))
            .frame(maxWidth: .infinity)
            .padding(.top, 60)
    }
}

private struct EmptyChart: View {
    var body: some View { Text("No plays in this period yet.").foregroundStyle(.secondary) }
}

private struct Tag: View {
    let text: String
    var color: Color?
    var plain = false

    var body: some View {
        Text(text)
            .font(.caption2.weight(.semibold))
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .foregroundStyle(color == nil ? Color.secondary : Color.white)
            .background(color?.opacity(0.85) ?? Color.white.opacity(plain ? 0.06 : 0.12), in: Capsule())
    }
}

private struct Avatar: View {
    let name: String
    let size: CGFloat

    var body: some View {
        Circle()
            .fill(Color.white.opacity(0.1))
            .frame(width: size, height: size)
            .overlay(Text(name.first.map { String($0).uppercased() } ?? "?").font(.system(size: size * 0.42, weight: .semibold)))
    }
}

private struct Legend: View {
    let items: [(String, Color)]

    var body: some View {
        if items.count > 1 {
            HStack(spacing: 14) {
                ForEach(items, id: \.0) { name, color in
                    HStack(spacing: 5) {
                        RoundedRectangle(cornerRadius: 2).fill(color).frame(width: 10, height: 10)
                        Text(name).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
        }
    }
}

private struct Meter: View {
    let fraction: Double
    let color: Color

    var body: some View {
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(.white.opacity(0.08))
                Capsule().fill(color).frame(width: max(4, g.size.width * min(1, fraction)))
            }
        }
        .frame(height: 8)
    }
}

/// Opens an item's page when there is one.
private struct ItemLink<Label: View>: View {
    let id: String?
    @ViewBuilder var label: Label

    var body: some View {
        if let id, !id.isEmpty {
            NavigationLink(value: ItemIDRoute(id: id)) { label }.buttonStyle(.plain)
        } else {
            label
        }
    }
}

/// Horizontal bars in one hue, labelled with the count.
private struct HBars: View {
    let rows: [StatCount]

    var body: some View {
        if rows.isEmpty {
            Text("Nothing in this period yet.").foregroundStyle(.secondary)
        } else {
            let maxCount = max(rows.map(\.count).max() ?? 1, 1)
            let total = max(rows.map(\.count).reduce(0, +), 1)
            VStack(spacing: 7) {
                ForEach(rows, id: \.name) { r in
                    let name = Fmt.method(r.name)
                    HStack(spacing: 10) {
                        Text(name).lineLimit(1).frame(width: 130, alignment: .leading)
                        Meter(fraction: Double(r.count) / Double(maxCount), color: StatColor.a)
                        Text(Fmt.num(r.count)).monospacedDigit().foregroundStyle(.secondary).frame(minWidth: 34, alignment: .trailing)
                    }
                    .font(.callout)
                    .help("\(name): \(r.count) (\(Int((Double(r.count) / Double(total) * 100).rounded()))%)")
                }
            }
        }
    }
}

/// One bar per player or viewer, split by stream type.
private struct SplitBars: View {
    let rows: [MethodSplit]

    var body: some View {
        if rows.isEmpty {
            Text("Nothing in this period yet.").foregroundStyle(.secondary)
        } else {
            let maxTotal = max(rows.map(\.total).max() ?? 1, 1)
            VStack(spacing: 7) {
                ForEach(rows, id: \.name) { r in
                    HStack(spacing: 10) {
                        Text(r.name).lineLimit(1).frame(width: 130, alignment: .leading)
                        GeometryReader { g in
                            let unit = g.size.width / CGFloat(maxTotal)
                            ZStack(alignment: .leading) {
                                Capsule().fill(.white.opacity(0.06))
                                HStack(spacing: 2) {
                                    ForEach([(r.direct, StatColor.direct), (r.remux, StatColor.remux), (r.transcode, StatColor.transcode)].filter { $0.0 > 0 }, id: \.0) { n, c in
                                        Rectangle().fill(c).frame(width: max(2, unit * CGFloat(n) - 2))
                                    }
                                }
                                .clipShape(Capsule())
                            }
                        }
                        .frame(height: 8)
                        Text(Fmt.num(r.total)).monospacedDigit().foregroundStyle(.secondary).frame(minWidth: 34, alignment: .trailing)
                    }
                    .font(.callout)
                    .help("\(r.name): \(r.direct) direct play, \(r.remux) direct stream, \(r.transcode) transcode")
                }
            }
        }
    }
}

private struct TopList: View {
    @Environment(AppState.self) private var state
    let rows: [TopTitle]
    var byUsers = false
    /// One viewer's list: no viewer counts.
    var own = false

    var body: some View {
        if rows.isEmpty {
            Text("Nothing in this period yet.").foregroundStyle(.secondary)
        } else {
            VStack(spacing: 10) {
                ForEach(Array(rows.prefix(10).enumerated()), id: \.offset) { i, t in
                    ItemLink(id: t.itemId) {
                        HStack(spacing: 12) {
                            Text("\(i + 1)").font(.callout.monospacedDigit()).foregroundStyle(.secondary).frame(width: 20)
                            RemoteImage(url: state.api?.imageURL(t.posterPath, size: "w92"))
                                .frame(width: 34, height: 51)
                                .clipShape(RoundedRectangle(cornerRadius: 4))
                            VStack(alignment: .leading, spacing: 2) {
                                Text(t.title).fontWeight(.semibold).lineLimit(1)
                                Text([t.year.map(String.init), own ? nil : "\(t.users) viewer\(t.users == 1 ? "" : "s")"].compactMap { $0 }.joined(separator: " · "))
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer(minLength: 4)
                            VStack(alignment: .trailing, spacing: 2) {
                                if byUsers {
                                    Text("\(Fmt.num(t.users)) viewer\(t.users == 1 ? "" : "s")").font(.callout.monospacedDigit())
                                    Text("\(Fmt.num(t.plays)) plays").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                                } else {
                                    Text("\(Fmt.num(t.plays)) play\(t.plays == 1 ? "" : "s")").font(.callout.monospacedDigit())
                                    Text("\(Fmt.hours(t.hours)) h").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                                }
                            }
                        }
                        .contentShape(Rectangle())
                    }
                }
            }
        }
    }
}

private struct TopUsers: View {
    @Environment(StatsNav.self) private var nav
    let rows: [TopUser]

    var body: some View {
        if rows.isEmpty {
            Text("Nothing in this period yet.").foregroundStyle(.secondary)
        } else {
            VStack(spacing: 10) {
                ForEach(Array(rows.prefix(10).enumerated()), id: \.offset) { i, u in
                    Button { nav.openViewer(u.userId) } label: {
                        HStack(spacing: 12) {
                            Text("\(i + 1)").font(.callout.monospacedDigit()).foregroundStyle(.secondary).frame(width: 20)
                            Avatar(name: u.name, size: 32)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(u.name).fontWeight(.semibold).lineLimit(1)
                                Text(u.lastPlayed).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                            }
                            Spacer(minLength: 4)
                            VStack(alignment: .trailing, spacing: 2) {
                                Text("\(Fmt.num(u.plays)) play\(u.plays == 1 ? "" : "s")").font(.callout.monospacedDigit())
                                Text("\(Fmt.hours(u.hours)) h").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            }
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }
}

// MARK: - Charts

private struct StackSeries {
    let name: String
    let color: Color
}

private struct StackPoint: Identifiable {
    let id: String
    let label: String
    /// The tooltip's heading.
    let title: String
    let values: [Int]
    var note: String?
}

/// Columns stacked by series, with a tooltip under the pointer.
private struct StackedBars: View {
    let points: [StackPoint]
    let series: [StackSeries]
    var labelEvery = 1
    var height: CGFloat = 160
    @State private var hover: String?

    private struct Bar: Identifiable {
        let id: String
        let x: String
        let series: String
        let value: Int
    }

    var body: some View {
        let bars = points.flatMap { p in
            p.values.enumerated().map { i, v in Bar(id: "\(p.id)|\(i)", x: p.id, series: series[i].name, value: v) }
        }
        let labels = Dictionary(uniqueKeysWithValues: points.map { ($0.id, $0.label) })
        let ticks = Set(points.enumerated().compactMap { $0.offset % max(1, labelEvery) == 0 ? $0.element.id : nil })
        Chart {
            ForEach(bars) { b in
                BarMark(x: .value("Day", b.x), y: .value("Plays", b.value))
                    .foregroundStyle(by: .value("Series", b.series))
                    .cornerRadius(2)
            }
            #if !os(macOS)
            if let hover, let p = points.first(where: { $0.id == hover }) {
                RuleMark(x: .value("Day", hover))
                    .foregroundStyle(Color.white.opacity(0.25))
                    .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                        tooltip(p)
                    }
            }
            #endif
        }
        .chartForegroundStyleScale(domain: series.map(\.name), range: series.map(\.color))
        .chartLegend(.hidden)
        .chartXAxis {
            AxisMarks { v in
                if let id = v.as(String.self), ticks.contains(id) {
                    AxisValueLabel { Text(labels[id] ?? "") }
                }
            }
        }
        .chartYAxis { AxisMarks(values: .automatic(desiredCount: 3)) }
        #if os(macOS)
        .modifier(BarHover(ids: points.map(\.id), selection: $hover) { id in
            if let p = points.first(where: { $0.id == id }) { tooltip(p) }
        })
        #else
        .chartXSelection(value: $hover)
        #endif
        .frame(height: height)
    }

    private func tooltip(_ p: StackPoint) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(p.title).font(.caption.bold())
            ForEach(Array(series.enumerated()), id: \.offset) { i, s in
                HStack(spacing: 6) {
                    RoundedRectangle(cornerRadius: 2).fill(s.color).frame(width: 8, height: 8)
                    Text(s.name)
                    Spacer(minLength: 10)
                    Text(Fmt.num(p.values[i])).monospacedDigit().bold()
                }
            }
            if let note = p.note { Text(note).foregroundStyle(.secondary) }
        }
        .font(.caption)
        .padding(8)
        .frame(minWidth: 140)
        .chartTooltip()
    }
}

#if os(macOS)
/// Hover for a category axis, which can't turn a position back into a value
/// on the Mac: the bar is found from how far across the pointer is. The
/// highlight is drawn over the chart rather than in it, since changing the
/// chart's marks under the pointer ends the hover.
private struct BarHover<Tip: View>: ViewModifier {
    let ids: [String]
    @Binding var selection: String?
    @ViewBuilder let tooltip: (String) -> Tip
    @State private var plot = CGRect.zero

    func body(content: Content) -> some View {
        content
            .chartOverlay { proxy in
                GeometryReader { geo in
                    let frame = proxy.plotFrame.map { geo[$0] } ?? .zero
                    Color.clear
                        .onAppear { plot = frame }
                        .onChange(of: frame) { _, f in plot = f }
                }
            }
            .overlay { GeometryReader { geo in highlight(width: geo.size.width) }.allowsHitTesting(false) }
            .onContinuousHover { phase in
                guard case .active(let p) = phase, plot.width > 0, !ids.isEmpty else {
                    selection = nil
                    return
                }
                let i = Int((p.x - plot.minX) / plot.width * CGFloat(ids.count))
                selection = ids.indices.contains(i) ? ids[i] : nil
            }
    }

    @ViewBuilder
    private func highlight(width: CGFloat) -> some View {
        if let id = selection, let i = ids.firstIndex(of: id), plot.width > 0 {
            let x = plot.minX + plot.width / CGFloat(ids.count) * (CGFloat(i) + 0.5)
            let right = x < width / 2
            ZStack(alignment: .topLeading) {
                Rectangle().fill(.white.opacity(0.25))
                    .frame(width: 1, height: plot.height)
                    .offset(x: x, y: plot.minY)
                HStack(spacing: 0) {
                    if right { Spacer().frame(width: x + 8) } else { Spacer(minLength: 0) }
                    tooltip(id).fixedSize()
                    if right { Spacer(minLength: 0) } else { Spacer().frame(width: width - x + 8) }
                }
                .frame(width: width)
                .offset(y: plot.minY)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
    }
}
#endif

private struct LineSeries {
    let name: String
    let color: Color
    let values: [Double]
}

/// Lines over time on one axis, with a crosshair and the values under it.
private struct LineChart: View {
    let times: [Date]
    let series: [LineSeries]
    let yMax: Double?
    let format: (Double) -> String
    let axis: (Double) -> String
    @State private var hover: Date?

    private struct Point: Identifiable {
        let id: Int
        let t: Date
        let series: String
        let value: Double
    }

    var body: some View {
        let points = series.enumerated().flatMap { si, s in
            zip(times, s.values).enumerated().map { i, p in Point(id: si * 100_000 + i, t: p.0, series: s.name, value: p.1) }
        }
        let top = max(yMax ?? 0, series.flatMap(\.values).max() ?? 0, 1)
        let index = hover.map(nearest)
        Chart {
            ForEach(points) { p in
                LineMark(x: .value("Time", p.t), y: .value("Value", p.value), series: .value("Series", p.series))
                    .foregroundStyle(by: .value("Series", p.series))
                    .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round, lineJoin: .round))
                    .interpolationMethod(.monotone)
            }
            if let i = index {
                RuleMark(x: .value("Time", times[i]))
                    .foregroundStyle(Color.white.opacity(0.3))
                    .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                        tooltip(i)
                    }
                ForEach(series, id: \.name) { s in
                    PointMark(x: .value("Time", times[i]), y: .value("Value", s.values[i]))
                        .foregroundStyle(by: .value("Series", s.name))
                        .symbolSize(50)
                }
            }
        }
        .chartForegroundStyleScale(domain: series.map(\.name), range: series.map(\.color))
        .chartLegend(.hidden)
        .chartYScale(domain: 0...top)
        .chartYAxis {
            AxisMarks(values: .automatic(desiredCount: 4)) { v in
                AxisGridLine()
                AxisValueLabel { if let d = v.as(Double.self) { Text(axis(d)) } }
            }
        }
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: 4)) { _ in
                AxisGridLine()
                AxisValueLabel(format: .dateTime.hour(.defaultDigits(amPM: .omitted)).minute())
            }
        }
        .chartHover($hover)
        .frame(height: 180)
    }

    private func nearest(_ d: Date) -> Int {
        var best = 0
        for (i, t) in times.enumerated() where abs(t.timeIntervalSince(d)) < abs(times[best].timeIntervalSince(d)) { best = i }
        return best
    }

    private func tooltip(_ i: Int) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(times[i].formatted(date: .omitted, time: .standard)).font(.caption.bold())
            ForEach(series, id: \.name) { s in
                HStack(spacing: 6) {
                    RoundedRectangle(cornerRadius: 2).fill(s.color).frame(width: 8, height: 8)
                    Text(s.name)
                    Spacer(minLength: 10)
                    Text(format(s.values[i])).monospacedDigit().bold()
                }
            }
        }
        .font(.caption)
        .padding(8)
        .frame(minWidth: 150)
        .chartTooltip()
    }
}

private extension View {
    /// The x value under the pointer on the Mac, or under a finger on iOS.
    @ViewBuilder
    func chartHover<P: Plottable>(_ selection: Binding<P?>) -> some View {
        #if os(macOS)
        chartOverlay { proxy in
            GeometryReader { geo in
                Rectangle().fill(.clear).contentShape(Rectangle())
                    .onContinuousHover { phase in
                        switch phase {
                        case .active(let p):
                            guard let plot = proxy.plotFrame else { return }
                            selection.wrappedValue = proxy.value(atX: p.x - geo[plot].origin.x, as: P.self)
                        case .ended:
                            selection.wrappedValue = nil
                        }
                    }
            }
        }
        #else
        chartXSelection(value: selection)
        #endif
    }

    /// A solid card: a material would sit over the chart as its own view on
    /// the Mac and take the pointer, ending the hover that showed it.
    func chartTooltip() -> some View {
        background(Color(white: 0.17).opacity(0.96), in: RoundedRectangle(cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(.white.opacity(0.1)))
            .allowsHitTesting(false)
    }

    func statCard(cornerRadius: CGFloat = 12) -> some View {
        background(.white.opacity(0.04), in: RoundedRectangle(cornerRadius: cornerRadius))
            .overlay(RoundedRectangle(cornerRadius: cornerRadius).stroke(.white.opacity(0.08)))
    }
}

// MARK: - Formatting

/// Series colours, checked for colour-blind separation on the dark surface.
private enum StatColor {
    static let direct = Color(red: 0x19 / 255, green: 0x9E / 255, blue: 0x70 / 255)
    static let remux = Color(red: 0x39 / 255, green: 0x87 / 255, blue: 0xE5 / 255)
    static let transcode = Color(red: 0xD9 / 255, green: 0x59 / 255, blue: 0x26 / 255)
    static let a = remux
    static let b = transcode
    static let c = direct

    static func method(_ m: String) -> Color {
        m == "transcode" ? transcode : m == "remux" ? remux : direct
    }
}

private enum Fmt {
    static func num(_ n: Int) -> String { n.formatted() }

    static func hours(_ h: Double) -> String {
        h >= 100 ? Int(h.rounded()).formatted() : h >= 10 ? String(Int(h.rounded())) : String(format: "%.1f", h)
    }

    static func rate(_ kbps: Int?) -> String {
        guard let k = kbps, k > 0 else { return "0 Mbps" }
        return k >= 1000 ? String(format: k >= 10000 ? "%.0f Mbps" : "%.1f Mbps", Double(k) / 1000) : "\(k) kbps"
    }

    static func size(_ bytes: Int64?, zero: String = "—") -> String {
        guard let bytes, bytes > 0 else { return zero }
        return ByteCountFormatter.string(fromByteCount: bytes, countStyle: .memory)
    }

    /// Axis labels for memory: whole gigabytes where they fit.
    static func gigabytes(_ b: Double) -> String {
        let gb = 1024.0 * 1024 * 1024
        if b <= 0 { return "0" }
        if b >= gb { return String(format: b >= 10 * gb ? "%.0f GB" : "%.1f GB", b / gb) }
        return "\(Int((b / 1024 / 1024).rounded())) MB"
    }

    static func uptime(_ s: Int64) -> String {
        let d = s / 86400, h = (s % 86400) / 3600, m = (s % 3600) / 60
        return d > 0 ? "\(d) d \(h) h" : h > 0 ? "\(h) h \(m) min" : "\(m) min"
    }

    nonisolated(unsafe) private static let relative: RelativeDateTimeFormatter = {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .short
        return f
    }()
    static func ago(_ unix: Int64) -> String {
        relative.localizedString(for: Date(timeIntervalSince1970: TimeInterval(unix)), relativeTo: .now)
    }

    static func method(_ m: String) -> String {
        switch m {
        case "direct": "Direct play"
        case "remux": "Direct stream"
        case "transcode": "Transcode"
        case "audio transcode": "Audio transcode"
        default: m
        }
    }

    static func res(_ w: Int?, _ h: Int?) -> String {
        let w = w ?? 0, h = h ?? 0
        if w == 0 && h == 0 { return "" }
        if w >= 3200 || h >= 1600 { return "4K" }
        if w >= 1700 || h >= 1000 { return "1080p" }
        if w >= 1100 || h >= 700 { return "720p" }
        if h >= 560 { return "576p" }
        if h >= 470 { return "480p" }
        return h > 0 ? "\(h)p" : "SD"
    }

    private static let codecs = [
        "hevc": "HEVC", "h265": "HEVC", "h264": "H.264", "avc": "H.264", "av1": "AV1", "vp9": "VP9", "vp8": "VP8", "mpeg2video": "MPEG-2",
        "mpeg4": "MPEG-4", "vc1": "VC-1", "aac": "AAC", "ac3": "Dolby Digital", "eac3": "Dolby Digital+", "truehd": "TrueHD", "dts": "DTS",
        "dts-hd": "DTS-HD", "flac": "FLAC", "opus": "Opus", "mp3": "MP3", "vorbis": "Vorbis", "pcm_s16le": "PCM", "pcm_s24le": "PCM", "alac": "ALAC",
    ]
    static func codec(_ c: String?) -> String {
        let c = c ?? ""
        return codecs[c.lowercased()] ?? c.uppercased()
    }

    static func container(_ c: String?) -> String {
        let c = (c ?? "").lowercased()
        if c.isEmpty { return "—" }
        if c.contains("matroska") { return "MKV" }
        if c.contains("mp4") || c.contains("mov") { return "MP4" }
        if c == "mpegts" { return "MPEG-TS" }
        return String(c.split(separator: ",").first ?? "").uppercased()
    }

    static func channels(_ n: Int?, _ layout: String?) -> String {
        if let layout, let r = layout.range(of: #"^\d\.\d"#, options: .regularExpression) {
            return String(layout[r.lowerBound...]).replacingOccurrences(of: #"\(.*\)"#, with: "", options: .regularExpression)
        }
        switch n ?? 0 {
        case 0: return ""
        case 1: return "Mono"
        case 2: return "Stereo"
        case 6: return "5.1"
        case 8: return "7.1"
        default: return "\(n!) ch"
        }
    }

    private static let iso2 = ["eng": "en", "spa": "es", "fre": "fr", "fra": "fr", "ger": "de", "deu": "de", "jpn": "ja", "ita": "it",
                               "por": "pt", "kor": "ko", "chi": "zh", "zho": "zh", "rus": "ru"]
    static func language(_ code: String?) -> String {
        guard let code, !code.isEmpty, code != "und" else { return "" }
        return Locale.current.localizedString(forLanguageCode: iso2[code] ?? code) ?? code
    }

    static func hw(_ k: String?) -> String {
        let names = ["videotoolbox": "VideoToolbox", "nvenc": "NVIDIA NVENC", "qsv": "Intel Quick Sync", "vaapi": "VA-API", "amf": "AMD AMF", "software": "Software"]
        return names[k ?? ""] ?? k ?? ""
    }

    static func os(_ o: String?) -> String {
        ["linux": "Linux", "darwin": "macOS", "windows": "Windows", "freebsd": "FreeBSD"][o ?? ""] ?? o ?? ""
    }

    static func isPrivate(_ ip: String) -> Bool {
        ip.range(of: #"^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|127\.|::1$|fe80:|fc|fd)"#, options: [.regularExpression, .caseInsensitive]) != nil
    }

    private static let dayParser: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        f.timeZone = .current
        return f
    }()
    private static func day(_ s: String) -> Date? { dayParser.date(from: s) }
    static func shortDay(_ s: String) -> String { day(s)?.formatted(.dateTime.month(.abbreviated).day()) ?? s }
    static func longDay(_ s: String) -> String { day(s)?.formatted(.dateTime.weekday(.abbreviated).month(.abbreviated).day().year()) ?? s }
    static func month(_ m: String, long: Bool = false) -> String {
        guard let d = day(m + "-01") else { return m }
        return long ? d.formatted(.dateTime.month(.wide).year()) : d.formatted(.dateTime.month(.abbreviated))
    }

    static func hour(_ h: Int) -> String {
        let d = Calendar.current.date(bySettingHour: h, minute: 0, second: 0, of: .now) ?? .now
        return d.formatted(.dateTime.hour())
    }

    /// Sunday first, like the server.
    static func weekday(_ i: Int, long: Bool = false) -> String {
        let names = long ? Calendar.current.weekdaySymbols : Calendar.current.shortWeekdaySymbols
        return names[i % 7]
    }
}

/// What a stats page says when it can't load. A server from before a page
/// existed answers 404, or sends figures in an older shape.
private func statsError(_ error: Error) -> String {
    if (error as? WrenTVError)?.status == 404 || error is DecodingError {
        return "This needs a newer Wren TV server. Update the server to see it."
    }
    return error.localizedDescription
}
