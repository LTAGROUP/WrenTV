import SwiftUI

/// When episodes air, a week at a time: a strip of the week's days on top
/// (click one to see just that day), then a shelf a day. The library's shows
/// still running and the ones being watched.
struct CalendarView: View {
    @Environment(AppState.self) private var state
    @State private var calendar: CalendarResponse?
    @State private var error: String?
    @State private var watchingOnly = false
    @State private var week = CalendarEpisode.week(of: CalendarEpisode.today)
    @State private var picked: String?

    var body: some View {
        let eps = (calendar?.episodes ?? []).filter { !watchingOnly || $0.watching }
        let days = CalendarEpisode.days(eps)
        let shown = days.filter { picked == nil || $0.date == picked }
        let next = shown.first { $0.date >= CalendarEpisode.today }?.episodes.first ?? shown.first?.episodes.first
        ScrollViewReader { proxy in
            CinematicPage(fallback: next?.heroEntry, fallbackBackdrop: next?.show.backdropPath, label: "Calendar") {
                WeekStrip(week: week, picked: $picked, counts: Dictionary(days.map { ($0.date, $0.episodes.count) }, uniquingKeysWith: +)) { step in
                    picked = nil
                    week = CalendarEpisode.week(of: CalendarEpisode.adding(7 * step, to: week[0]))
                }
                .frame(maxWidth: 760, alignment: .leading)
                .padding(.horizontal, 28)
                if calendar != nil {
                    if shown.isEmpty {
                        ContentUnavailableView(emptyTitle, systemImage: "calendar",
                                               description: Text("Episodes show here for shows in the library that are still running. Use the arrows to see other weeks."))
                            .padding(.top, 40)
                    }
                    ForEach(shown, id: \.date) { day in
                        Shelf(title: CalendarEpisode.dayName(day.date), subtitle: "\(day.episodes.count) episode\(day.episodes.count == 1 ? "" : "s")") {
                            ForEach(day.episodes) { e in
                                NavigationLink(value: ItemIDRoute(id: e.itemId ?? e.show.id)) { CalendarCard(episode: e) }
                                    .buttonStyle(.plain)
                            }
                        }
                        .id(day.date)
                    }
                } else if let error {
                    ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error)).padding(.top, 60)
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 80)
                }
            }
            // Open this week on today, with what aired earlier in the week above it.
            .onChange(of: calendar?.from) {
                guard picked == nil, let d = days.first(where: { $0.date >= CalendarEpisode.today })?.date, d != days.first?.date else { return }
                Task { @MainActor in proxy.scrollTo(d, anchor: .top) }
            }
        }
        .navigationTitle("Calendar")
        .toolbar {
            ToolbarItemGroup {
                if !week.contains(CalendarEpisode.today) {
                    Button("This Week") {
                        picked = nil
                        week = CalendarEpisode.week(of: CalendarEpisode.today)
                    }
                    .help("Go back to this week")
                }
                Picker("Shows", selection: $watchingOnly) {
                    Text("All Shows").tag(false)
                    Text("Shows I'm Watching").tag(true)
                }
                .pickerStyle(.segmented)
                .help("Which shows to list")
            }
        }
        .task(id: "\(week[0])|\(state.refreshTick)") {
            do {
                let c = try await state.api?.calendar(from: week.first, to: week.last)
                if c?.from == week.first { calendar = c }
                error = nil
            } catch is CancellationError {
            } catch {
                if calendar == nil { self.error = error.localizedDescription }
            }
        }
    }

    private var emptyTitle: String {
        if let picked { return "Nothing airing \(CalendarEpisode.dayName(picked) == "Today" ? "today" : "on " + CalendarEpisode.dayName(picked))" }
        return watchingOnly ? "Nothing this week for your shows" : "Nothing airing this week"
    }
}

/// The seven days of a week with how many episodes air each, between
/// previous and next week buttons. Clicking a day shows just that day;
/// clicking it again shows the whole week.
struct WeekStrip: View {
    let week: [String]
    @Binding var picked: String?
    let counts: [String: Int]
    let step: (Int) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(range).font(.subheadline.weight(.semibold)).foregroundStyle(.secondary)
            HStack(spacing: 6) {
                arrow("chevron.left", "Previous week", -1)
                ForEach(week, id: \.self) { d in
                    Button { picked = picked == d ? nil : d } label: { cell(d) }
                        .buttonStyle(.plain)
                        .accessibilityLabel("\(CalendarEpisode.dayName(d)), \(counts[d] ?? 0) episodes")
                        .accessibilityAddTraits(picked == d ? .isSelected : [])
                }
                arrow("chevron.right", "Next week", 1)
            }
        }
    }

    private var range: String {
        let p = CalendarEpisode.parser
        guard let a = week.first.flatMap(p.date), let b = week.last.flatMap(p.date) else { return "" }
        return "\(a.formatted(.dateTime.month(.abbreviated).day())) – \(b.formatted(.dateTime.month(.abbreviated).day().year()))"
    }

    private func arrow(_ icon: String, _ label: String, _ n: Int) -> some View {
        Button { step(n) } label: {
            Image(systemName: icon).font(.body.weight(.semibold)).frame(width: 28).frame(maxHeight: .infinity)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(label)
        .help(label)
    }

    private func cell(_ d: String) -> some View {
        let date = CalendarEpisode.parser.date(from: d) ?? Date()
        let on = picked == d, today = d == CalendarEpisode.today, n = counts[d] ?? 0
        return VStack(spacing: 2) {
            Text(date.formatted(.dateTime.weekday(.abbreviated)).uppercased())
                .font(.system(size: 10, weight: .bold)).kerning(0.6)
                .foregroundStyle(on ? .white : today ? Color.wrenEmber : .secondary)
            Text(date.formatted(.dateTime.day())).font(.title3.weight(.bold)).opacity(n == 0 && !on ? 0.55 : 1)
            Text(n > 0 ? "\(n)" : " ").font(.caption2).foregroundStyle(on ? .white : .secondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 8)
        .background(on ? Color.wrenEmber : .white.opacity(0.05), in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(today && !on ? Color.wrenEmber : .white.opacity(0.1)))
        .contentShape(Rectangle())
        .hoverLift(cornerRadius: 10)
    }
}

private struct CalendarCard: View {
    @Environment(AppState.self) private var state
    let episode: CalendarEpisode
    var width: CGFloat = PosterSize.width(280)

    var body: some View {
        let e = episode
        VStack(alignment: .leading, spacing: 6) {
            RemoteImage(url: state.api?.imageURL(e.stillPath ?? e.show.backdropPath, size: "w500"))
                .frame(width: width, height: width * 9 / 16)
                .overlay(alignment: .topLeading) {
                    if e.upNext || e.watched {
                        Text(e.upNext ? "UP NEXT" : "WATCHED")
                            .font(.system(size: 9, weight: .bold)).kerning(0.6)
                            .padding(.horizontal, 8).padding(.vertical, 4)
                            .background(e.upNext ? Color.wrenEmber : .black.opacity(0.7), in: Capsule())
                            .padding(10)
                    }
                }
                .clipShape(RoundedRectangle(cornerRadius: 10))
                .hoverLift(cornerRadius: 10)
                .ambient(e.show.backdropPath) { e.heroEntry }
            Text(e.show.title).font(.callout.weight(.medium)).lineLimit(1)
            Text("\(e.code) · \(e.name)").font(.caption).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(width: width)
        .contentShape(Rectangle())
    }
}

extension CalendarEpisode {
    var heroEntry: HeroEntry {
        var meta = ["\(code) · \(name)", airs]
        if upNext { meta.append("Up next") } else if watched { meta.append("Watched") }
        if itemId == nil && date <= CalendarEpisode.today { meta.append("Not in the library yet") }
        return HeroEntry(id: "cal:\(id)", title: show.title, logo: show.logoPath,
                         meta: meta.joined(separator: "  ·  "), overview: overview?.isEmpty == false ? overview : show.overview)
    }
}
