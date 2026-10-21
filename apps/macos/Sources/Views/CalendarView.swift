import SwiftUI

/// When episodes air, a shelf a day, from a week back to a month ahead: the
/// library's shows still running and the ones being watched.
struct CalendarView: View {
    @Environment(AppState.self) private var state
    @State private var calendar: CalendarResponse?
    @State private var error: String?
    @State private var watchingOnly = false

    var body: some View {
        let eps = (calendar?.episodes ?? []).filter { !watchingOnly || $0.watching }
        let next = eps.first { $0.date >= CalendarEpisode.today } ?? eps.last
        ScrollViewReader { proxy in
            CinematicPage(fallback: next?.heroEntry, fallbackBackdrop: next?.show.backdropPath, label: "Calendar") {
                if calendar != nil {
                    if eps.isEmpty {
                        ContentUnavailableView(watchingOnly ? "Nothing coming up for your shows" : "Nothing airing", systemImage: "calendar",
                                               description: Text("Episodes from a week back to a month ahead show here, for shows in the library that are still running."))
                            .padding(.top, 60)
                    }
                    ForEach(CalendarEpisode.days(eps), id: \.date) { day in
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
            // Open on what's next to air, with what just aired above it.
            .onChange(of: calendar?.from) {
                guard let d = next?.date else { return }
                Task { @MainActor in proxy.scrollTo(d, anchor: .top) }
            }
        }
        .navigationTitle("Calendar")
        .toolbar {
            ToolbarItem {
                Picker("Shows", selection: $watchingOnly) {
                    Text("All Shows").tag(false)
                    Text("Shows I'm Watching").tag(true)
                }
                .pickerStyle(.segmented)
                .help("Which shows to list")
            }
        }
        .task(id: state.refreshTick) {
            do {
                calendar = try await state.api?.calendar()
                error = nil
            } catch is CancellationError {
            } catch {
                if calendar == nil { self.error = error.localizedDescription }
            }
        }
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
