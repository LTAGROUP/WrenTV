import Charts
import SwiftUI

/// Who's watching what: now playing, plays over time, top titles and viewers,
/// and what the library holds.
struct StatsView: View {
    @Environment(AppState.self) private var state
    @AppStorage("statsDays") private var days = 30
    @State private var data: StatsResponse?
    @State private var error: String?

    var body: some View {
        ScrollView {
            if let data {
                VStack(alignment: .leading, spacing: 18) {
                    nowPlaying(data.nowPlaying ?? [])
                    kpis([("\(data.stats.plays)", "Plays"), (hours(data.stats.hours), "Hours watched"),
                          ("\(data.stats.users)", "Active viewers"), ("\(data.stats.titles)", "Titles watched")])
                    if data.stats.plays == 0 {
                        Panel("Activity") {
                            Text("No plays in this period yet.").foregroundStyle(.secondary).frame(maxWidth: .infinity, minHeight: 80)
                        }
                    } else {
                        Panel("Plays per day") { dailyChart(data.stats.daily) }
                        HStack(alignment: .top, spacing: 18) {
                            Panel("By time of day") { hourChart(data.stats.byHour) }
                            Panel("By day of week") { weekdayChart(data.stats.byWeekday) }
                        }
                    }
                    HStack(alignment: .top, spacing: 18) {
                        Panel("Most watched movies") { TopList(rows: data.stats.topMovies ?? []) }
                        Panel("Most watched shows") { TopList(rows: data.stats.topShows ?? []) }
                    }
                    HStack(alignment: .top, spacing: 18) {
                        Panel("Top viewers") { topUsers(data.stats.topUsers ?? []) }
                        Panel("How it played") {
                            Bars(rows: (data.stats.byMethod ?? []).map { StatCount(name: methodName($0.name), count: $0.count) })
                            Text("Players").font(.headline).padding(.top, 8)
                            Bars(rows: data.stats.byClient ?? [])
                        }
                    }
                    Panel("Recent plays") { recent(data.recent ?? []) }

                    Text("Library").font(.title2.bold()).padding(.top, 8)
                    kpis([("\(data.library.movies)", "Movies"), ("\(data.library.shows)", "Shows"),
                          ("\(data.library.episodes)", "Episodes"), (ByteCountFormatter.string(fromByteCount: data.library.bytes, countStyle: .file), "On disk")])
                    HStack(alignment: .top, spacing: 18) {
                        Panel("Resolution") {
                            Bars(rows: data.library.resolutions ?? [])
                            Text("Dynamic range").font(.headline).padding(.top, 8)
                            Bars(rows: data.library.hdr ?? [])
                        }
                        Panel("Video codec") {
                            Bars(rows: data.library.videoCodecs ?? [])
                            Text("Container").font(.headline).padding(.top, 8)
                            Bars(rows: data.library.containers ?? [])
                        }
                    }
                }
                .padding(28)
            } else if let error {
                ContentUnavailableView("Couldn't load stats", systemImage: "exclamationmark.triangle", description: Text(error))
                    .padding(.top, 120)
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 200)
            }
        }
        .navigationTitle("Stats")
        .toolbar {
            Picker("Period", selection: $days) {
                Text("7 days").tag(7)
                Text("30 days").tag(30)
                Text("90 days").tag(90)
                Text("1 year").tag(365)
            }
            .pickerStyle(.segmented)
        }
        .task(id: "\(days)|\(state.refreshTick)") {
            // Refresh while open so Now Playing stays current.
            while !Task.isCancelled {
                await load()
                try? await Task.sleep(for: .seconds(15))
            }
        }
    }

    private func load() async {
        do { data = try await state.api?.stats(days: days); error = nil } catch { self.error = error.localizedDescription }
    }

    // MARK: Sections

    @ViewBuilder
    private func nowPlaying(_ np: [NowPlaying]) -> some View {
        Panel(np.isEmpty ? "Now playing" : "Now playing · \(np.count)") {
            if np.isEmpty {
                Text("Nobody is watching right now.").foregroundStyle(.secondary)
            } else {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 320), spacing: 14)], spacing: 14) {
                    ForEach(np) { s in NowPlayingCard(session: s) { Task { await state.api?.stopSession(s.sessionId); await load() } } }
                }
            }
        }
    }

    private func kpis(_ values: [(String, String)]) -> some View {
        HStack(spacing: 14) {
            ForEach(values, id: \.1) { v, label in
                VStack(alignment: .leading, spacing: 4) {
                    Text(v).font(.system(size: 28, weight: .bold).monospacedDigit())
                    Text(label.uppercased()).font(.caption.weight(.semibold)).kerning(0.6).foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(14)
                .cardBackground(cornerRadius: 10)
            }
        }
    }

    private func dailyChart(_ daily: [DayStat]) -> some View {
        Chart(daily, id: \.date) { d in
            BarMark(x: .value("Day", date(d.date) ?? .now, unit: .day), y: .value("Plays", d.plays))
                .foregroundStyle(Color.wrenEmber)
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: 3, topTrailingRadius: 3))
        }
        .chartYAxis { AxisMarks(values: .automatic(desiredCount: 3)) }
        .frame(height: 170)
        .accessibilityLabel("Plays per day")
    }

    private func hourChart(_ hours: [Int]) -> some View {
        Chart(Array(hours.enumerated()), id: \.offset) { h, n in
            BarMark(x: .value("Hour", h), y: .value("Plays", n))
                .foregroundStyle(Color.wrenEmber)
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: 3, topTrailingRadius: 3))
        }
        .chartXAxis {
            AxisMarks(values: [0, 6, 12, 18]) { v in
                AxisGridLine()
                AxisValueLabel { if let h = v.as(Int.self) { Text(hourLabel(h)) } }
            }
        }
        .chartYAxis { AxisMarks(values: .automatic(desiredCount: 3)) }
        .frame(height: 130)
    }

    private func weekdayChart(_ week: [Int]) -> some View {
        let names = Calendar.current.shortWeekdaySymbols // Sunday first, like the server
        return Chart(Array(week.enumerated()), id: \.offset) { i, n in
            BarMark(x: .value("Day", names[i % 7]), y: .value("Plays", n), width: .ratio(0.6))
                .foregroundStyle(Color.wrenEmber)
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: 3, topTrailingRadius: 3))
        }
        .chartYAxis { AxisMarks(values: .automatic(desiredCount: 3)) }
        .frame(height: 130)
    }

    @ViewBuilder
    private func topUsers(_ users: [TopUser]) -> some View {
        if users.isEmpty {
            Text("Nothing in this period yet.").foregroundStyle(.secondary)
        } else {
            Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 8) {
                GridRow {
                    Text("Viewer"); Text("Plays").gridColumnAlignment(.trailing); Text("Hours").gridColumnAlignment(.trailing); Text("Last watched")
                }
                .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                ForEach(users, id: \.userId) { u in
                    GridRow {
                        Text(u.name).fontWeight(.semibold)
                        Text("\(u.plays)").monospacedDigit()
                        Text(hours(u.hours)).monospacedDigit()
                        VStack(alignment: .leading, spacing: 1) {
                            Text(u.lastPlayed).lineLimit(1)
                            Text(relative(u.lastSeen)).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func recent(_ rows: [HistoryRow]) -> some View {
        if rows.isEmpty {
            Text("Nothing in this period yet.").foregroundStyle(.secondary)
        } else {
            Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 8) {
                GridRow {
                    Text("Title"); Text("Viewer"); Text("Player"); Text("Playback"); Text("Watched").gridColumnAlignment(.trailing); Text("When")
                }
                .font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                ForEach(rows) { r in
                    GridRow {
                        Text(r.showTitle.map { "\($0) — \(r.title)" } ?? r.title).lineLimit(1)
                        Text(r.userName).lineLimit(1)
                        Text(r.client).lineLimit(1).foregroundStyle(.secondary)
                        Text(methodName(r.method)).foregroundStyle(.secondary)
                        HStack(spacing: 4) {
                            if r.completed { Image(systemName: "checkmark.circle.fill").foregroundStyle(Color.wrenEmber) }
                            Text(formatDuration(r.playedSeconds)).monospacedDigit()
                        }
                        Text(relative(r.startedAt)).foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    // MARK: Formatting

    private func hours(_ h: Double) -> String { h >= 10 ? String(Int(h.rounded())) : String(format: "%.1f", h) }

    private func hourLabel(_ h: Int) -> String { h == 0 ? "12 AM" : h < 12 ? "\(h) AM" : h == 12 ? "12 PM" : "\(h - 12) PM" }

    private func methodName(_ m: String) -> String {
        switch m {
        case "direct": "Direct Play"
        case "remux": "Direct Stream"
        case "transcode": "Transcode"
        default: m.capitalized
        }
    }

    private func relative(_ unix: Int64) -> String {
        RelativeDateTimeFormatter().localizedString(for: Date(timeIntervalSince1970: TimeInterval(unix)), relativeTo: .now)
    }

    private func date(_ s: String) -> Date? {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        f.timeZone = .current
        return f.date(from: s)
    }
}

/// Horizontal bars: one hue, labelled with the count.
private struct Bars: View {
    let rows: [StatCount]

    var body: some View {
        if rows.isEmpty {
            Text("Nothing yet.").foregroundStyle(.secondary)
        } else {
            let maxCount = max(rows.map(\.count).max() ?? 1, 1)
            let total = max(rows.map(\.count).reduce(0, +), 1)
            VStack(spacing: 7) {
                ForEach(rows, id: \.name) { r in
                    HStack(spacing: 10) {
                        Text(r.name).lineLimit(1).frame(width: 130, alignment: .leading)
                        GeometryReader { g in
                            ZStack(alignment: .leading) {
                                Capsule().fill(.white.opacity(0.06))
                                Capsule().fill(Color.wrenEmber).frame(width: max(4, g.size.width * CGFloat(r.count) / CGFloat(maxCount)))
                            }
                        }
                        .frame(height: 8)
                        Text("\(r.count)").monospacedDigit().foregroundStyle(.secondary).frame(minWidth: 30, alignment: .trailing)
                    }
                    .font(.callout)
                    .help("\(r.name): \(r.count) (\(Int((Double(r.count) / Double(total) * 100).rounded()))%)")
                }
            }
        }
    }
}

private struct TopList: View {
    @Environment(AppState.self) private var state
    let rows: [TopTitle]

    var body: some View {
        if rows.isEmpty {
            Text("Nothing in this period yet.").foregroundStyle(.secondary)
        } else {
            VStack(spacing: 10) {
                ForEach(Array(rows.prefix(8).enumerated()), id: \.offset) { i, t in
                    HStack(spacing: 12) {
                        Text("\(i + 1)").font(.callout.monospacedDigit()).foregroundStyle(.secondary).frame(width: 18)
                        RemoteImage(url: state.api?.imageURL(t.posterPath, size: "w92"))
                            .frame(width: 34, height: 51)
                            .clipShape(RoundedRectangle(cornerRadius: 4))
                        VStack(alignment: .leading, spacing: 2) {
                            Text(t.title).fontWeight(.semibold).lineLimit(1)
                            Text([t.year.map(String.init), "\(t.users) viewer\(t.users == 1 ? "" : "s")"].compactMap { $0 }.joined(separator: " · "))
                                .font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        VStack(alignment: .trailing, spacing: 2) {
                            Text("\(t.plays) play\(t.plays == 1 ? "" : "s")").font(.callout.monospacedDigit())
                            Text(String(format: "%.1f h", t.hours)).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }
}

private struct NowPlayingCard: View {
    @Environment(AppState.self) private var state
    let session: NowPlaying
    let stop: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            RemoteImage(url: state.api?.imageURL(session.posterPath, size: "w185"))
                .frame(width: 60, height: 90)
                .clipShape(RoundedRectangle(cornerRadius: 6))
            VStack(alignment: .leading, spacing: 4) {
                Text(session.title).fontWeight(.semibold).lineLimit(1)
                if let s = session.subtitle { Text(s).font(.caption).foregroundStyle(.secondary).lineLimit(1) }
                Text("\(session.userName) · \(session.client)").font(.caption).lineLimit(1)
                Text(session.quality).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                ProgressView(value: session.duration > 0 ? min(session.position / session.duration, 1) : 0)
                    .tint(Color.wrenEmber)
                HStack {
                    Text("\(formatClock(session.position)) / \(formatClock(session.duration))").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    Spacer()
                    Button("Stop", role: .destructive, action: stop).controlSize(.small)
                }
            }
        }
        .padding(12)
        .background(.white.opacity(0.04), in: RoundedRectangle(cornerRadius: 10))
    }
}
