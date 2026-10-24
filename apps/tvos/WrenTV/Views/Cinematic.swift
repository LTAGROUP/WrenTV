import SwiftUI

/*
 * Home, Movies, TV Shows, libraries, hubs and collections share one
 * cinematic layout: the focused title's details in a hero at the top, over
 * its artwork, and rows of titles below. The hero stays put; the rows
 * scroll under it so the focused row always settles on the same line.
 */

/// What the hero shows: the focused title, or the page's own details.
struct HeroEntry: Equatable {
    var id: String
    var title: String
    var backdrop: String?
    var logo: String?
    var meta: String
    var overview: String?
    var label: String?
    /// With no backdrop, an image the page takes only a soft colour from.
    var wash: String?
}

/// The focused title and row of a cinematic page, set by its cards.
@MainActor
@Observable
final class Ambient {
    var entry: HeroEntry?
    var row: String?

    func show(_ e: HeroEntry, row: String?) {
        if entry != e { withAnimation(.easeOut(duration: 0.25)) { entry = e } }
        if let row, self.row != row { self.row = row }
    }

    /// Back to the page's own hero.
    func clear() {
        if entry != nil { withAnimation(.easeOut(duration: 0.25)) { entry = nil } }
    }
}

private struct CinematicRowKey: EnvironmentKey {
    static let defaultValue: String? = nil
}

extension EnvironmentValues {
    /// The cinematic row a card sits in, so focusing it settles that row.
    var cinematicRow: String? {
        get { self[CinematicRowKey.self] }
        set { self[CinematicRowKey.self] = newValue }
    }
}

extension View {
    /// Shows entry in the page's hero while this card has focus.
    func ambient(_ entry: @escaping () -> HeroEntry) -> some View { modifier(AmbientFocus(entry: entry)) }

    /// Puts the page's own hero back while this has focus.
    func ambientHome() -> some View { modifier(AmbientFocus(entry: nil)) }
}

private struct AmbientFocus: ViewModifier {
    @Environment(Ambient.self) private var ambient: Ambient?
    @Environment(\.cinematicRow) private var row
    let entry: (() -> HeroEntry)?
    @FocusState private var focused: Bool

    func body(content: Content) -> some View {
        content
            .focused($focused)
            .onChange(of: focused) { _, on in
                guard on else { return }
                if let entry { ambient?.show(entry(), row: row) } else { ambient?.clear() }
            }
    }
}

/// Card sizes on cinematic pages: smaller than a grid's, so the hero has room.
/// The hero is the same height on every row, so the page never jumps.
enum Cinematic {
    /// Posters and tiles scale with the Poster Size setting.
    static var poster: CGFloat { 190 * PosterSize.current.scale }
    static var landscape: CGFloat { 360 * PosterSize.current.scale }
    static let heroHeight: CGFloat = 480
    static let gridSpacing: CGFloat = 44
    /// Posters across the screen.
    static var columns: Int { Int((1920 - TV.gutter * 2 + gridSpacing) / (poster + gridSpacing)) }
}

/// A cinematic page. fallback fills the hero until something takes focus.
struct CinematicPage<Rows: View>: View {
    @Environment(AppState.self) private var state
    var fallback: HeroEntry?
    var label: String?
    @ViewBuilder var rows: () -> Rows
    @State private var ambient = Ambient()

    var body: some View {
        let entry = ambient.entry ?? fallback
        ZStack(alignment: .topLeading) {
            backdrop(entry)
            VStack(alignment: .leading, spacing: 0) {
                HeroText(entry: entry, label: label)
                    .frame(height: Cinematic.heroHeight, alignment: .bottomLeading)
                    .padding(.horizontal, TV.gutter)
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) { rows() }
                            .padding(.top, 10)
                            .padding(.bottom, 500)
                    }
                    .scrollIndicators(.hidden)
                    .onChange(of: ambient.row) { _, row in
                        guard let row else { return }
                        withAnimation(.easeOut(duration: 0.3)) { proxy.scrollTo(row, anchor: .top) }
                    }
                }
            }
        }
        .environment(ambient)
        .ignoresSafeArea()
    }

    private func backdrop(_ entry: HeroEntry?) -> some View {
        ZStack {
            if let path = entry?.backdrop {
                RemoteImage(url: state.api?.imageURL(path, size: "w1280"))
                    .id(path)
                    .transition(.opacity)
            } else if let wash = entry?.wash {
                // Blurred to nothing but its colours, and kept faint.
                RemoteImage(url: state.api?.imageURL(wash, size: "w185"))
                    .blur(radius: 140)
                    .opacity(0.32)
                    .id(wash)
                    .transition(.opacity)
            }
            // Only as wide as the text needs, so the art stays the focus.
            LinearGradient(stops: [.init(color: Color.wrenInk.opacity(0.8), location: 0), .init(color: Color.wrenInk.opacity(0.3), location: 0.32),
                                   .init(color: .clear, location: 0.58)], startPoint: .leading, endPoint: .trailing)
            LinearGradient(stops: [.init(color: .clear, location: 0), .init(color: Color.wrenInk.opacity(0.7), location: 0.45),
                                   .init(color: Color.wrenInk, location: 0.75)], startPoint: .top, endPoint: .bottom)
        }
        .frame(width: 1920, height: 1080)
        .animation(.easeOut(duration: 0.4), value: entry?.backdrop)
    }
}

/// The focused title's name, details and summary, low on the hero so it sits just above the rows.
private struct HeroText: View {
    @Environment(AppState.self) private var state
    let entry: HeroEntry?
    let label: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if let e = entry {
                if let label { Text(label.uppercased()).font(.caption.weight(.bold)).foregroundStyle(Color.wrenEmber) }
                if let logo = e.logo {
                    RemoteImage(url: state.api?.imageURL(logo, size: "w500"), contentMode: .fit)
                        .frame(maxWidth: 420, maxHeight: 104, alignment: .bottomLeading)
                } else {
                    Text(e.title).font(.system(size: 58, weight: .bold)).lineLimit(1)
                }
                if !e.meta.isEmpty { Text(e.meta).font(.callout).foregroundStyle(.secondary) }
                if let o = e.overview, !o.isEmpty {
                    Text(o).font(.subheadline).lineLimit(3).frame(maxWidth: 900, alignment: .leading)
                        .shadow(color: .black.opacity(0.5), radius: 10)
                }
            }
        }
        .padding(.bottom, 24)
        .id(entry?.id)
        .transition(.opacity)
        .animation(.easeOut(duration: 0.2), value: entry?.id)
    }
}

/// A row of a cinematic page: focusing anything in it settles it under the hero.
struct CinematicRow<Content: View>: View {
    let id: String
    @ViewBuilder var content: () -> Content

    var body: some View {
        content()
            .environment(\.cinematicRow, id)
            .id(id)
    }
}

/// Lines of posters, each a row of the page, for more titles than one row can hold.
struct PosterLines<T: Identifiable, Card: View>: View {
    let id: String
    let title: String
    var subtitle: String?
    let entries: [T]
    @ViewBuilder var card: (T) -> Card

    var body: some View {
        let lines = stride(from: 0, to: entries.count, by: Cinematic.columns).map { Array(entries[$0..<min($0 + Cinematic.columns, entries.count)]) }
        ForEach(Array(lines.enumerated()), id: \.offset) { i, line in
            CinematicRow(id: "\(id):\(i)") {
                VStack(alignment: .leading, spacing: 4) {
                    if i == 0 { ShelfTitle(title: title, subtitle: subtitle) }
                    HStack(alignment: .top, spacing: Cinematic.gridSpacing) {
                        ForEach(line) { card($0) }
                    }
                    .padding(.horizontal, TV.gutter)
                    .padding(.top, i == 0 ? 30 : 0)
                    .padding(.bottom, 30)
                }
                .focusSection()
            }
        }
    }
}

// MARK: Layout pages

/// A page made of the server's layout rows: Home, Movies, TV Shows or a
/// library. Servers from before layouts answer 404, and get fallback.
struct LayoutPageView<Fallback: View>: View {
    @Environment(AppState.self) private var state
    let scope: String
    var label: String?
    @ViewBuilder var fallback: () -> Fallback
    @State private var page: LayoutPage?
    @State private var error: String?
    @State private var unsupported = false
    /// Rows per request; the rest load as the page scrolls.
    private let pageRows = 6

    var body: some View {
        Group {
            if unsupported {
                fallback()
            } else {
                CinematicPage(fallback: first, label: label) {
                    if let p = page {
                        LayoutRows(rows: p.rows)
                        if p.nextOffset != nil {
                            ProgressView().frame(maxWidth: .infinity).padding(.vertical, 30)
                                .task(id: p.nextOffset) { await loadMore() }
                        }
                        if p.rows.isEmpty {
                            EmptyState(title: "Nothing here yet",
                                       message: state.isAdmin ? "Add a library from the Wren TV app on a computer or in a web browser." : "Ask the server owner to share a library.")
                        }
                    } else if let error {
                        EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
                    } else {
                        ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
                    }
                }
            }
        }
        .task(id: "\(scope)|\(state.refreshTick)") { await load() }
    }

    /// Until a title takes focus, the hero shows the first.
    private var first: HeroEntry? {
        page?.rows.lazy.compactMap { r in
            r.items?.first?.heroEntry ?? r.titles?.first?.heroEntry ?? r.hubs?.first?.heroEntry
        }.first
    }

    private func load() async {
        guard let api = state.api else { return }
        do {
            // A refresh keeps as many rows as were showing, so the page doesn't jump.
            page = try await api.layout(scope, limit: max(pageRows, page?.rows.count ?? 0))
            error = nil
        } catch let e as WrenTVError where e.status == 404 {
            unsupported = true
        } catch {
            if page == nil { self.error = error.localizedDescription }
        }
    }

    /// The next rows, once the last ones scroll into view.
    private func loadMore() async {
        guard let api = state.api, let next = page?.nextOffset else { return }
        guard let more = try? await api.layout(scope, offset: next, limit: pageRows), var cur = page, cur.nextOffset == next else { return }
        let have = Set(cur.rows.map(\.id))
        cur.rows += more.rows.filter { !have.contains($0.id) }
        cur.nextOffset = more.nextOffset
        page = cur
    }
}

/// A hub: a genre, a streaming service or an admin's own, as shelves.
struct HubView: View {
    @Environment(AppState.self) private var state
    let route: HubRoute
    @State private var page: HubPage?
    @State private var error: String?

    var body: some View {
        let hub = page?.hub ?? HubCard(id: route.id, name: route.name, kind: "custom")
        CinematicPage(fallback: hub.heroEntry, label: hub.kindLabel) {
            if let p = page {
                LayoutRows(rows: p.rows)
                if p.rows.isEmpty { EmptyState(title: "Nothing here yet", message: "None of this hub's shelves found any titles.") }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: "\(route.id)|\(state.refreshTick)") {
            do {
                page = try await state.api?.hub(route.id)
                error = nil
            } catch {
                if page == nil { self.error = error.localizedDescription }
            }
        }
    }
}

/// The server's rows, each drawn the way its kind asks.
struct LayoutRows: View {
    let rows: [LayoutRow]

    var body: some View {
        ForEach(rows) { row in
            CinematicRow(id: row.id) {
                Shelf(title: row.title, subtitle: row.subtitle) {
                    switch row.kind {
                    case "landscape":
                        ForEach((row.items ?? []).uniqued()) { LandscapeCard(item: $0, width: Cinematic.landscape) }
                    case "hubs":
                        ForEach((row.hubs ?? []).uniqued()) { HubTile(hub: $0, logos: row.style == "logos") }
                    case "titles":
                        ForEach(Array((row.titles ?? []).uniqued().enumerated()), id: \.element.id) { i, t in
                            TitleCard(title: t, rank: row.ranked == true && i < 10 ? i + 1 : nil, width: Cinematic.poster)
                        }
                    default:
                        ForEach((row.items ?? []).uniqued()) { PosterCard(item: $0, width: Cinematic.poster) }
                        ForEach((row.missing ?? []).uniqued()) { TitleCard(title: $0, width: Cinematic.poster) }
                    }
                    // A collection's or hub's row leads to the rest of it.
                    if let c = row.collection {
                        // When the shelf already shows everything the card still opens the page, without "View All".
                        MoreCard(label: row.more == true ? "View All" : "Open Collection", value: c)
                    } else if let hub = row.hubId {
                        MoreCard(label: "Open \(row.title)", value: HubRoute(id: hub, name: row.title))
                    }
                }
            }
        }
    }
}

/// A hub's tile: its artwork with the name over it, or a service's logo on its colour.
struct HubTile: View {
    @Environment(AppState.self) private var state
    let hub: HubCard
    var logos = false

    var body: some View {
        NavigationLink(value: HubRoute(id: hub.id, name: hub.name)) {
            ZStack(alignment: .bottomLeading) {
                if logos, let logo = hub.logoPath {
                    (Color(hex: hub.color) ?? Color.wrenSurface)
                    RemoteImage(url: state.api?.imageURL(logo, size: "w300"), contentMode: .fit)
                        .padding(44)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    RemoteImage(url: state.api?.imageURL(hub.backdropPath, size: "w780"))
                    LinearGradient(colors: [.clear, (Color(hex: hub.color) ?? .black).opacity(0.85)], startPoint: .center, endPoint: .bottom)
                    Text(hub.name).font(.headline.bold()).lineLimit(2).padding(20)
                }
            }
            .frame(width: Cinematic.landscape, height: Cinematic.landscape * 9 / 16)
        }
        .buttonStyle(.card)
        .ambient { hub.heroEntry }
    }
}

/// The card at the end of a row that opens the whole of it.
private struct MoreCard<V: Hashable>: View {
    let label: String
    let value: V

    var body: some View {
        NavigationLink(value: value) {
            VStack(spacing: 14) {
                Image(systemName: "arrow.right").font(.title2)
                Text(label).font(.caption).multilineTextAlignment(.center).lineLimit(2)
            }
            .padding(16)
            .frame(width: Cinematic.poster, height: Cinematic.poster * 1.5)
            .background(Color.wrenSurface)
        }
        .buttonStyle(.card)
    }
}

// MARK: Collections

/// Every collection the viewer can see, a shelf per kind: the server's own,
/// franchises, lists, charts and smart collections.
struct CollectionsView: View {
    @Environment(AppState.self) private var state
    @State private var collections: [WrenCollection]?
    @State private var error: String?

    var body: some View {
        let groups = grouped
        CinematicPage(fallback: collections?.first?.heroEntry(count: nil), label: "Collections") {
            if let list = collections {
                if list.isEmpty {
                    EmptyState(title: "No collections yet",
                               message: state.isAdmin ? "Create collections in the web app: hand-picked, smart rules, or synced from TMDB."
                                                      : "Collections the server owner makes show up here.",
                               symbol: "rectangle.stack")
                }
                ForEach(groups, id: \.name) { g in
                    CinematicRow(id: "group:\(g.name)") {
                        Shelf(title: g.name, subtitle: "\(g.collections.count) collection\(g.collections.count == 1 ? "" : "s")") {
                            ForEach(g.collections) { CollectionTile(collection: $0) }
                        }
                    }
                }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: state.refreshTick) {
            do {
                collections = try await state.api?.collections()
                error = nil
            } catch {
                if collections == nil { self.error = error.localizedDescription }
            }
        }
    }

    /// In the server's order, which already puts the groups in theirs.
    private var grouped: [(name: String, collections: [WrenCollection])] {
        var out: [(name: String, collections: [WrenCollection])] = []
        for c in collections ?? [] {
            let g = c.group ?? "Collections"
            if let i = out.firstIndex(where: { $0.name == g }) { out[i].collections.append(c) } else { out.append((g, [c])) }
        }
        return out
    }
}

/// A collection's wide tile: its artwork with the name over it.
struct CollectionTile: View {
    @Environment(AppState.self) private var state
    let collection: WrenCollection

    var body: some View {
        let w = Cinematic.landscape
        VStack(alignment: .leading, spacing: 10) {
            NavigationLink(value: collection) {
                ZStack(alignment: .bottomLeading) {
                    RemoteImage(url: state.api?.imageURL(collection.backdropPath ?? collection.posterPath, size: "w780"))
                    LinearGradient(colors: [.clear, .black.opacity(0.8)], startPoint: .center, endPoint: .bottom)
                    Text(collection.name).font(.headline.bold()).lineLimit(2).padding(20)
                    if collection.active == false {
                        Text("OUT OF SEASON").font(.caption2.bold()).padding(.horizontal, 10).padding(.vertical, 4)
                            .background(.black.opacity(0.7), in: .capsule)
                            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading).padding(14)
                    }
                }
                .frame(width: w, height: w * 9 / 16)
            }
            .buttonStyle(.card)
            .ambient { collection.heroEntry(count: collection.itemCount.map { "\($0) titles" }) }
            Text(collection.itemCount.map { "\($0) titles · \(collection.kindLabel)" } ?? collection.kindLabel)
                .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(width: w)
    }
}

// MARK: Calendar

/// When episodes air, a week at a time: a strip of the week's days on top
/// (select one to see just that day), then a shelf a day. The library's
/// shows still running and the ones being watched.
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
        let next = eps.first { $0.date >= CalendarEpisode.today } ?? eps.last
        CinematicPage(fallback: next?.heroEntry, label: "Calendar") {
            CinematicRow(id: "filter") {
                HStack(spacing: 30) {
                    Picker("Shows", selection: $watchingOnly) {
                        Text("All Shows").tag(false)
                        Text("Shows I'm Watching").tag(true)
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 720)
                    if !week.contains(CalendarEpisode.today) {
                        Button("This Week") {
                            picked = nil
                            week = CalendarEpisode.week(of: CalendarEpisode.today)
                        }
                    }
                }
                .padding(.horizontal, TV.gutter)
                .padding(.vertical, 20)
                .focusSection()
            }
            CinematicRow(id: "week") {
                TVWeekStrip(week: week, picked: $picked, counts: Dictionary(days.map { ($0.date, $0.episodes.count) }, uniquingKeysWith: +)) { step in
                    picked = nil
                    week = CalendarEpisode.week(of: CalendarEpisode.adding(7 * step, to: week[0]))
                }
            }
            if calendar != nil {
                if shown.isEmpty {
                    EmptyState(title: emptyTitle,
                               message: "Episodes show here for shows in the library that are still running. Use the arrows to see other weeks.",
                               symbol: "calendar")
                }
                ForEach(shown, id: \.date) { day in
                    CinematicRow(id: "day:\(day.date)") {
                        Shelf(title: CalendarEpisode.dayName(day.date), subtitle: "\(day.episodes.count) episode\(day.episodes.count == 1 ? "" : "s")") {
                            ForEach(day.episodes) { CalendarCard(episode: $0) }
                        }
                    }
                }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
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
/// previous and next week buttons. Selecting a day shows just that day;
/// selecting it again shows the whole week.
private struct TVWeekStrip: View {
    let week: [String]
    @Binding var picked: String?
    let counts: [String: Int]
    let step: (Int) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(range).font(.callout.weight(.semibold)).foregroundStyle(.secondary)
            HStack(spacing: 18) {
                Button { step(-1) } label: { Image(systemName: "chevron.left").frame(width: 60, height: 120) }
                    .buttonStyle(WeekCellStyle(on: false, today: false))
                    .accessibilityLabel("Previous week")
                ForEach(week, id: \.self) { d in
                    Button { picked = picked == d ? nil : d } label: { cell(d) }
                        .buttonStyle(WeekCellStyle(on: picked == d, today: d == CalendarEpisode.today))
                        .accessibilityLabel("\(CalendarEpisode.dayName(d)), \(counts[d] ?? 0) episodes")
                }
                Button { step(1) } label: { Image(systemName: "chevron.right").frame(width: 60, height: 120) }
                    .buttonStyle(WeekCellStyle(on: false, today: false))
                    .accessibilityLabel("Next week")
            }
        }
        .padding(.horizontal, TV.gutter)
        .padding(.vertical, 20)
        .focusSection()
    }

    private var range: String {
        let p = CalendarEpisode.parser
        guard let a = week.first.flatMap(p.date), let b = week.last.flatMap(p.date) else { return "" }
        return "\(a.formatted(.dateTime.month(.abbreviated).day())) – \(b.formatted(.dateTime.month(.abbreviated).day().year()))"
    }

    private func cell(_ d: String) -> some View {
        let date = CalendarEpisode.parser.date(from: d) ?? Date()
        let n = counts[d] ?? 0
        return VStack(spacing: 4) {
            Text(date.formatted(.dateTime.weekday(.abbreviated)).uppercased()).font(.caption2.bold())
            Text(date.formatted(.dateTime.day())).font(.title2.bold()).opacity(n == 0 && picked != d ? 0.55 : 1)
            Text(n > 0 ? "\(n)" : " ").font(.caption2)
        }
        .frame(width: 150, height: 120)
    }
}

/// A day in the week strip: ember when picked, outlined when today, lifted
/// and white when focused.
private struct WeekCellStyle: ButtonStyle {
    let on: Bool
    let today: Bool

    func makeBody(configuration: Configuration) -> some View {
        Cell(configuration: configuration, on: on, today: today)
    }

    private struct Cell: View {
        let configuration: Configuration
        let on: Bool
        let today: Bool
        @Environment(\.isFocused) private var focused

        var body: some View {
            configuration.label
                .foregroundStyle(focused ? .black : .white)
                .background(focused ? Color.white : on ? Color.wrenEmber : .white.opacity(0.08), in: RoundedRectangle(cornerRadius: 16))
                .overlay(RoundedRectangle(cornerRadius: 16).stroke(today && !on && !focused ? Color.wrenEmber : .clear, lineWidth: 3))
                .scaleEffect(focused ? 1.08 : configuration.isPressed ? 0.97 : 1)
                .shadow(color: .black.opacity(focused ? 0.4 : 0), radius: 14, y: 8)
                .animation(.easeOut(duration: 0.15), value: focused)
        }
    }
}

private struct CalendarCard: View {
    @Environment(AppState.self) private var state
    let episode: CalendarEpisode

    var body: some View {
        let e = episode, w = Cinematic.landscape
        VStack(alignment: .leading, spacing: 10) {
            NavigationLink(value: ItemRoute(id: e.itemId ?? e.show.id, title: e.itemId == nil ? e.show.title : e.name)) {
                RemoteImage(url: state.api?.imageURL(e.stillPath ?? e.show.backdropPath, size: "w780"))
                    .frame(width: w, height: w * 9 / 16)
                    .overlay(alignment: .topLeading) {
                        if e.upNext || e.watched {
                            Text(e.upNext ? "UP NEXT" : "WATCHED").font(.caption2.bold()).padding(.horizontal, 10).padding(.vertical, 4)
                                .background(e.upNext ? Color.wrenEmber : .black.opacity(0.7), in: .capsule)
                                .padding(14)
                        }
                    }
            }
            .buttonStyle(.card)
            .ambient { e.heroEntry }
            Text(e.show.title).font(.caption).lineLimit(1)
            Text("\(e.code) · \(e.name)").font(.caption2).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(width: w)
    }
}

private let collectionSorts: [(String, String)] = [
    ("", "Collection order"), ("title", "Title"), ("year", "Year (newest)"), ("year_asc", "Year (oldest)"),
    ("rating", "Rating"), ("added", "Recently added"),
]

// MARK: Person

/// An actor or crew member: photo, details and biography up top over a soft
/// colour from their photo, then rows of what they're in with the role under
/// each title. A focused title brings its artwork in; back up at the
/// biography, the page is theirs again. Selecting a title opens its page.
struct PersonView: View {
    @Environment(AppState.self) private var state
    let route: PersonRoute
    @State private var person: Person?
    @State private var error: String?
    @State private var bioOpen = false

    var body: some View {
        let facts = (person?.facts ?? []).filter { !$0.hasPrefix("Known for") }
        CinematicPage(fallback: HeroEntry(id: "person:\(route.id)", title: person?.name ?? route.name,
                                          // Not their best-known title's art: that reads as if its poster had focus.
                                          backdrop: nil, meta: facts.joined(separator: "  ·  "),
                                          overview: person?.biography, wash: person?.profilePath),
                      label: person?.knownFor ?? "Person") {
            CinematicRow(id: "about") {
                HStack(alignment: .top, spacing: 40) {
                    RemoteImage(url: state.api?.imageURL(person?.profilePath, size: "w500"))
                        .overlay {
                            if person != nil && person?.profilePath == nil {
                                Text(String(route.name.prefix(1))).font(.largeTitle.bold()).foregroundStyle(.secondary)
                            }
                        }
                        .frame(width: 200, height: 300)
                        .background(.white.opacity(0.06))
                        .clipShape(RoundedRectangle(cornerRadius: 16))
                    if let bio = person?.biography, !bio.isEmpty {
                        Button { bioOpen = true } label: {
                            VStack(alignment: .leading, spacing: 10) {
                                Text("Biography").font(.callout.bold())
                                Text(bio).font(.callout).lineLimit(5).multilineTextAlignment(.leading)
                                Text("Select to read more").font(.caption2).foregroundStyle(.secondary)
                            }
                            .frame(maxWidth: 1100, alignment: .leading)
                            .padding(24)
                        }
                        .buttonStyle(.card)
                        .ambientHome()
                    }
                }
                .padding(.horizontal, TV.gutter)
                .padding(.vertical, 24)
                .focusSection()
            }
            if let p = person {
                ForEach(p.rows, id: \.title) { r in
                    CinematicRow(id: "row:\(r.title)") {
                        Shelf(title: r.title, subtitle: "\(r.credits.count)") {
                            ForEach(r.credits.uniqued()) { TitleCard(title: $0) }
                        }
                    }
                }
                if p.rows.isEmpty {
                    EmptyState(title: "No movies or shows listed", symbol: "film")
                }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .sheet(isPresented: $bioOpen) {
            BioReader(name: person?.name ?? route.name, text: person?.biography ?? "")
        }
        .task(id: route.id) {
            do {
                person = try await state.api?.person(route.id)
            } catch is CancellationError {
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

/// A collection as one shelf in its order, loaded a page at a time as it's
/// browsed toward the end.
struct CollectionView: View {
    @Environment(AppState.self) private var state
    let collection: WrenCollection
    @State private var sort = ""

    private var pager: CollectionPager { CollectionPager.of(collection.id, sort: sort) }

    var body: some View {
        let pager = pager
        let c = pager.collection ?? collection
        CinematicPage(fallback: c.heroEntry(count: pager.countLabel.isEmpty ? nil : pager.countLabel), label: "Collection") {
            CinematicRow(id: "sort") {
                HStack {
                    Menu {
                        Picker("Order", selection: $sort) {
                            ForEach(collectionSorts, id: \.0) { Text($0.1).tag($0.0) }
                        }
                    } label: {
                        Label(collectionSorts.first { $0.0 == sort }?.1 ?? "Collection order", systemImage: "arrow.up.arrow.down")
                    }
                }
                .padding(.horizontal, TV.gutter)
                .padding(.vertical, 20)
                .focusSection()
            }
            if !pager.entries.isEmpty {
                CinematicRow(id: "shelf:\(sort)") {
                    Shelf(title: c.name, subtitle: pager.total > 0 ? "\(pager.total)" : nil) {
                        ForEach(Array(pager.entries.enumerated()), id: \.element.entryKey) { i, t in
                            TitleCard(title: t, width: Cinematic.poster)
                                .onAppear { if let api = state.api { pager.appeared(i, api) } }
                        }
                        if pager.error != nil, let api = state.api {
                            Button { pager.retry(api) } label: {
                                VStack(spacing: 12) {
                                    Image(systemName: "arrow.clockwise").font(.title2)
                                    Text("Couldn't load more").font(.caption)
                                }
                                .frame(width: Cinematic.poster, height: Cinematic.poster * 1.5)
                            }
                            .buttonStyle(.card)
                        }
                    }
                }
            } else if let error = pager.error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else if pager.done {
                EmptyState(title: "Nothing in this collection yet")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: "\(collection.id)|\(sort)|\(state.refreshTick)") {
            if let api = state.api { await pager.start(api, refresh: state.refreshTick) }
        }
    }
}

/// Opens the watchlist page.
struct WatchlistRoute: Hashable {}

/// Movies and shows saved for later, newest added first: the banner over
/// them, then a row of each. Watched ones are hidden unless asked for.
struct WatchlistView: View {
    @Environment(AppState.self) private var state
    @State private var items: [Item]?
    @AppStorage("watchlistHideWatched") private var hideWatched = true

    var body: some View {
        let shown = (items ?? []).filter { !hideWatched || !$0.isWatched }
        let hero = HeroEntry(id: "watchlist", title: "Watchlist", backdrop: shown.first { $0.backdropPath != nil }?.backdropPath,
                             meta: items == nil ? "" : "\(shown.count) title\(shown.count == 1 ? "" : "s")",
                             overview: "Movies and shows you saved for later, newest first.")
        CinematicPage(fallback: hero, label: "Watchlist") {
            CinematicRow(id: "watched") {
                HStack {
                    Toggle(isOn: $hideWatched) { Label("Hide Watched", systemImage: "eye.slash") }
                        .fixedSize()
                }
                .padding(.horizontal, TV.gutter)
                .padding(.vertical, 20)
                .focusSection()
            }
            if let items {
                ForEach([("Movies", shown.filter { $0.kind == "movie" }), ("TV Shows", shown.filter { $0.kind != "movie" })], id: \.0) { title, list in
                    if !list.isEmpty {
                        CinematicRow(id: title) {
                            Shelf(title: title, subtitle: "\(list.count)") { ForEach(list) { PosterCard(item: $0, width: Cinematic.poster) } }
                        }
                    }
                }
                if shown.isEmpty {
                    EmptyState(title: items.isEmpty ? "Your watchlist is empty" : "All watched",
                               message: items.isEmpty ? "Add movies and shows from their pages to find them here." : "Turn off Hide Watched to see them.",
                               symbol: "bookmark")
                }
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: state.refreshTick) {
            if let list = try? await state.api?.watchlist() { items = list }
        }
    }
}

/// The last card of a shelf that shows only some of a page: opens it all.
struct SeeAllCard<Route: Hashable>: View {
    let route: Route

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            NavigationLink(value: route) {
                Image(systemName: "arrow.right").font(.title)
                    .frame(width: TV.poster, height: TV.poster * 1.5)
            }
            .buttonStyle(.card)
            Text("See all").font(.caption)
        }
        .frame(width: TV.poster)
    }
}

// MARK: Hero entries

extension Item {
    var heroEntry: HeroEntry {
        HeroEntry(id: id, title: kind == "episode" ? (showTitle ?? title) : title, backdrop: backdropPath ?? stillPath,
                  logo: kind == "episode" ? nil : logoPath, meta: metaLine, overview: overview)
    }
}

extension TitleView {
    var heroEntry: HeroEntry {
        if let item { return item.heroEntry }
        var parts: [String] = []
        if let year { parts.append(String(year)) }
        parts.append(mediaType == "show" ? "Series" : "Movie")
        if let r = rating, r > 0 { parts.append(String(format: "★ %.1f", r)) }
        return HeroEntry(id: id, title: title, backdrop: backdropPath, meta: parts.joined(separator: "  ·  "), overview: overview)
    }
}

extension HubCard {
    var heroEntry: HeroEntry {
        HeroEntry(id: "hub:\(id)", title: name, backdrop: backdropPath, meta: kindLabel, overview: tagline)
    }
}

extension WrenCollection {
    func heroEntry(count: String?) -> HeroEntry {
        HeroEntry(id: "collection:\(id)", title: name, backdrop: backdropPath,
                  meta: [count, kindLabel].compactMap { $0 }.joined(separator: "  ·  "),
                  overview: description.isEmpty ? nil : description)
    }
}

extension CalendarEpisode {
    var heroEntry: HeroEntry {
        var meta = ["\(code) · \(name)", airs]
        if upNext { meta.append("Up next") } else if watched { meta.append("Watched") }
        if itemId == nil && date <= CalendarEpisode.today { meta.append("Not in the library yet") }
        return HeroEntry(id: "cal:\(id)", title: show.title, backdrop: show.backdropPath, logo: show.logoPath,
                         meta: meta.joined(separator: "  ·  "), overview: overview?.isEmpty == false ? overview : show.overview)
    }
}

extension Color {
    /// "#rrggbb", or nil.
    init?(hex: String?) {
        guard var s = hex?.trimmingCharacters(in: .whitespaces), !s.isEmpty else { return nil }
        if s.hasPrefix("#") { s.removeFirst() }
        guard s.count == 6, let v = Int(s, radix: 16) else { return nil }
        self.init(red: Double((v >> 16) & 0xFF) / 255, green: Double((v >> 8) & 0xFF) / 255, blue: Double(v & 0xFF) / 255)
    }
}

/// A long biography read with the remote: each paragraph takes focus in
/// turn, so swiping down moves through the text a screenful at a time.
private struct BioReader: View {
    let name: String
    let text: String
    @FocusState private var focused: Int?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                Text(name).font(.title2.bold())
                ForEach(Array(bioChunks(text).enumerated()), id: \.offset) { i, chunk in
                    Text(chunk).font(.body)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.vertical, 6)
                        .opacity(focused == nil || focused == i ? 1 : 0.6)
                        .focusable()
                        .focused($focused, equals: i)
                }
            }
            .frame(maxWidth: 1300, alignment: .leading)
            .padding(60)
        }
        .onAppear { focused = 0 }
    }
}

/// The text's paragraphs, with any too long for a screen split between sentences.
func bioChunks(_ text: String, limit: Int = 700) -> [String] {
    text.components(separatedBy: "\n").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }.flatMap { para -> [String] in
        guard para.count > limit else { return [para] }
        var out: [String] = []
        var cur = ""
        para.enumerateSubstrings(in: para.startIndex..., options: .bySentences) { sentence, _, _, _ in
            guard let sentence else { return }
            if !cur.isEmpty && cur.count + sentence.count > limit { out.append(cur.trimmingCharacters(in: .whitespaces)); cur = "" }
            cur += sentence
        }
        if !cur.isEmpty { out.append(cur.trimmingCharacters(in: .whitespaces)) }
        return out
    }
}

// MARK: Studio

/// The studios behind a title, as logo tiles that open each studio's page.
struct StudioTiles: View {
    let studios: [Studio]

    var body: some View {
        Shelf(title: "Production") {
            ForEach(studios) { s in
                NavigationLink(value: StudioRoute(id: s.id, name: s.name)) { StudioLogo(studio: s, width: 300) }
                    .buttonStyle(.card)
            }
        }
    }
}

/// A studio's logo on a light tile, or its name where there's no logo.
struct StudioLogo: View {
    @Environment(AppState.self) private var state
    let studio: Studio
    var width: CGFloat

    var body: some View {
        ZStack {
            Color(white: 0.95)
            if let url = state.api?.imageURL(studio.logoPath, size: "w300") {
                AsyncImage(url: url) { $0.resizable().scaledToFit() } placeholder: { Color.clear }
                    .padding(width * 0.14)
            } else {
                Text(studio.name).font(.headline).foregroundStyle(.black).multilineTextAlignment(.center).padding(14)
            }
        }
        .frame(width: width, height: width / 2)
    }
}

/// A studio's movies and shows over their artwork: popular, top rated and
/// recent movies, then popular and top rated shows. Each shelf loads more as
/// it nears its end.
struct StudioView: View {
    @Environment(AppState.self) private var state
    let route: StudioRoute
    @State private var company: Company?
    @State private var shelves: [StudioShelf] = []
    @State private var error: String?

    var body: some View {
        let facts = [company?.headquarters, company?.originCountry].compactMap { $0 }.filter { !$0.isEmpty }
        CinematicPage(fallback: HeroEntry(id: "studio:\(route.id)", title: company?.name ?? route.name,
                                          backdrop: company?.backdropPath, meta: facts.joined(separator: "  ·  "),
                                          overview: company?.description),
                      label: "Studio") {
            if company != nil {
                ForEach(shelves) { s in
                    CinematicRow(id: "row:\(s.id)") {
                        Shelf(title: s.row.title) {
                            ForEach(s.items) { t in TitleCard(title: t).onAppear { s.appeared(t, api: state.api) } }
                        }
                    }
                }
                if shelves.isEmpty {
                    EmptyState(title: "No movies or shows listed", symbol: "film")
                }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: route.id) {
            guard company == nil else { return }
            do {
                guard let c = try await state.api?.company(route.id) else { return }
                shelves = c.rows.map { StudioShelf(row: $0, studio: c.id) }
                company = c
            } catch is CancellationError {
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}
