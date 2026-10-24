import SwiftUI

/// A TMDB title: links to the library item when the server has it. Without
/// it, streaming servers open it to play; others link to TMDB.
struct TitleCard: View {
    @Environment(AppState.self) private var state
    let title: TitleView
    var rank: Int?
    var width: CGFloat = PosterSize.width(150)

    var body: some View {
        if let item = title.item {
            NavigationLink(value: item) { card }
                .buttonStyle(.plain)
                .contextMenu { ItemContextMenu(item: item) }
        } else if state.canStream {
            NavigationLink(value: StreamTitleRoute(tmdbId: title.tmdbId, mediaType: title.mediaType, title: title.title)) { card }
                .buttonStyle(.plain)
        } else {
            Link(destination: title.tmdbURL) { card }
                .buttonStyle(.plain)
                .help("Not in your library — opens TMDB")
        }
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 6) {
            RemoteImage(url: state.api?.imageURL(title.posterPath))
                .frame(width: width, height: width * 1.5)
                .overlay {
                    if title.posterPath == nil {
                        Text(title.title).font(.headline).multilineTextAlignment(.center).padding(8)
                    }
                }
                .overlay(alignment: .topLeading) {
                    if let rank {
                        Text("\(rank)")
                            .font(.caption.bold().monospacedDigit())
                            .padding(.horizontal, 7).padding(.vertical, 3)
                            .background(.black.opacity(0.7), in: RoundedRectangle(cornerRadius: 5))
                            .padding(6)
                    }
                }
                .overlay(alignment: .topTrailing) {
                    if title.item == nil && title.watched == true {
                        Image(systemName: "checkmark.circle.fill")
                            .symbolRenderingMode(.palette)
                            .foregroundStyle(.white, Color.wrenEmber)
                            .padding(6)
                    }
                }
                .overlay(alignment: .bottom) {
                    if title.item == nil && !state.canStream {
                        Text("NOT IN LIBRARY")
                            .font(.system(size: 9, weight: .bold))
                            .kerning(0.6)
                            .padding(.vertical, 4)
                            .frame(maxWidth: .infinity)
                            .background(.black.opacity(0.7))
                    } else if let p = title.item?.progress, title.item?.inProgress == true {
                        ProgressStrip(fraction: p.fraction)
                    }
                }
                .clipShape(RoundedRectangle(cornerRadius: 8))
                .opacity(title.item == nil && !state.canStream ? 0.8 : 1)
                .hoverLift()
                .ambient(title.backdropPath ?? title.item?.backdropPath) { title.heroEntry }
                // Ready the hero's logo before the card is hovered.
                .task(id: title.id) { state.logos.want(title) }
            Text(title.title).font(.callout).lineLimit(1)
            let sub = [title.role ?? "", title.year.map(String.init) ?? ""].filter { !$0.isEmpty }.joined(separator: " · ")
            if !sub.isEmpty { Text(sub).font(.caption).foregroundStyle(.secondary).lineLimit(1) }
        }
        .frame(width: width)
        .contentShape(Rectangle())
    }
}

/// Rotten Tomatoes, IMDb, Metacritic and TMDB scores.
struct RatingsRow: View {
    let ratings: Ratings

    var body: some View {
        HStack(spacing: 8) {
            if let rt = ratings.rottenTomatoes, rt > 0 {
                pill {
                    Image(systemName: rt >= 60 ? "circle.fill" : "burst.fill")
                        .foregroundStyle(rt >= 60 ? Color.wrenEmber : .green)
                        .font(.caption)
                    Text("\(rt)%").bold()
                }
                .help("Rotten Tomatoes (critics)")
            }
            if let imdb = ratings.imdb, imdb > 0 {
                pill {
                    Text("IMDb").font(.caption2.weight(.heavy)).foregroundStyle(.black)
                        .padding(.horizontal, 4).padding(.vertical, 1)
                        .background(Color(red: 0.96, green: 0.77, blue: 0.09), in: RoundedRectangle(cornerRadius: 3))
                    Text(String(format: "%.1f", imdb)).bold()
                    if let v = ratings.imdbVotes, v > 0 { Text(compact(v)).foregroundStyle(.secondary).font(.caption) }
                }
            }
            if let mc = ratings.metacritic, mc > 0 {
                pill {
                    Text("MC").font(.caption2.weight(.heavy))
                    Text("\(mc)").bold()
                }
                .help("Metacritic")
            }
            if let t = ratings.tmdb, t > 0 {
                pill {
                    Text("TMDB").font(.caption2.weight(.heavy)).foregroundStyle(Color(red: 0.0, green: 0.82, blue: 0.85))
                    Text("\(Int((t * 10).rounded()))%").bold()
                }
            }
        }
    }

    private func pill<C: View>(@ViewBuilder _ c: () -> C) -> some View {
        HStack(spacing: 5) { c() }
            .font(.callout)
            .padding(.horizontal, 9).padding(.vertical, 5)
            .background(.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 7))
            .overlay(RoundedRectangle(cornerRadius: 7).stroke(.white.opacity(0.12)))
    }

    private func compact(_ n: Int) -> String {
        n >= 1_000_000 ? String(format: "%.1fM", Double(n) / 1e6) : n >= 1000 ? "\(n / 1000)K" : "\(n)"
    }
}

struct CastShelf: View {
    @Environment(AppState.self) private var state
    let cast: [CastMember]

    var body: some View {
        Shelf(title: "Cast") {
            ForEach(Array(cast.enumerated()), id: \.offset) { _, c in
                if let id = c.id {
                    NavigationLink(value: PersonRoute(id: id, name: c.name)) { face(c) }
                        .buttonStyle(.plain)
                        .help("See \(c.name)'s movies and shows")
                } else {
                    face(c)
                }
            }
        }
    }

    private func face(_ c: CastMember) -> some View {
                VStack(spacing: 6) {
                    RemoteImage(url: state.api?.imageURL(c.profilePath, size: "w185"))
                        .overlay {
                            if c.profilePath == nil {
                                Text(initials(c.name)).font(.title2.bold()).foregroundStyle(.secondary)
                            }
                        }
                        .frame(width: 96, height: 96)
                        .clipShape(Circle())
                        .overlay(Circle().stroke(.white.opacity(0.12)))
                    Text(c.name).font(.callout.weight(.medium)).lineLimit(1)
                    if let ch = c.character, !ch.isEmpty {
                        Text(ch).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                .frame(width: 112)
                .contentShape(Rectangle())
                .hoverLift()
    }

    private func initials(_ name: String) -> String {
        name.split(separator: " ").prefix(2).compactMap(\.first).map(String.init).joined()
    }
}

/// An actor or crew member over their best-known title's artwork: photo,
/// details and biography, then rows of what they're in with the role under
/// each title.
struct PersonView: View {
    let route: PersonRoute

    var body: some View {
        PersonBody(route: route).ambientBackdrop()
    }
}

private struct PersonBody: View {
    @Environment(AppState.self) private var state
    @Environment(\.ambient) private var ambient
    let route: PersonRoute
    @State private var person: Person?
    @State private var error: String?
    @State private var bioOpen = false

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 30) {
                header
                if let p = person {
                    ForEach(p.rows, id: \.title) { r in
                        Shelf(title: r.title, subtitle: "\(r.credits.count)") {
                            ForEach(r.credits.uniqued()) { TitleCard(title: $0) }
                        }
                    }
                    if p.rows.isEmpty {
                        ContentUnavailableView("No movies or shows listed", systemImage: "film")
                    }
                } else if let error {
                    ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 60)
                }
            }
            .padding(.bottom, 28)
        }
        .navigationTitle(person?.name ?? route.name)
        .task(id: route.id) {
            do {
                person = try await state.api?.person(route.id)
                ambient?.path = person?.backdropPath
            } catch is CancellationError {
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 24) {
            RemoteImage(url: state.api?.imageURL(person?.profilePath, size: "w500"))
                .overlay {
                    if person != nil && person?.profilePath == nil {
                        Text(route.name.split(separator: " ").prefix(2).compactMap(\.first).map(String.init).joined())
                            .font(.largeTitle.bold()).foregroundStyle(.secondary)
                    }
                }
                .frame(width: 180, height: 270)
                .background(.white.opacity(0.05))
                .clipShape(RoundedRectangle(cornerRadius: 12))
                .overlay(RoundedRectangle(cornerRadius: 12).stroke(.white.opacity(0.12)))
            VStack(alignment: .leading, spacing: 10) {
                if let k = person?.knownFor, !k.isEmpty {
                    Text(k.uppercased()).font(.caption.weight(.bold)).kerning(1.2).foregroundStyle(Color.wrenEmber)
                }
                Text(person?.name ?? route.name).font(.system(size: 40, weight: .bold)).lineLimit(2)
                let facts = (person?.facts ?? []).filter { !$0.hasPrefix("Known for") }
                if !facts.isEmpty {
                    Text(facts.joined(separator: "  ·  ")).foregroundStyle(.secondary)
                }
                if let bio = person?.biography, !bio.isEmpty {
                    Text(bio).lineLimit(bioOpen ? nil : 6).foregroundStyle(.white.opacity(0.85)).frame(maxWidth: 720, alignment: .leading)
                        .textSelection(.enabled)
                    Button(bioOpen ? "Less" : "More") { withAnimation { bioOpen.toggle() } }
                        .buttonStyle(.link)
                }
                HStack(spacing: 10) {
                    if let imdb = person?.imdbId, let u = URL(string: "https://www.imdb.com/name/\(imdb)/") { Link("IMDb ↗", destination: u) }
                    if let u = URL(string: "https://www.themoviedb.org/person/\(route.id)") { Link("TMDB ↗", destination: u) }
                }
                .font(.callout)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 28)
        .padding(.top, 40)
    }
}

/// Placeholder shelf while a row loads.
struct SkeletonShelf: View {
    let title: String
    var count = 7
    @State private var pulse = false

    var body: some View {
        Shelf(title: title) {
            ForEach(0..<count, id: \.self) { _ in
                VStack(alignment: .leading, spacing: 6) {
                    RoundedRectangle(cornerRadius: 8).fill(.white.opacity(pulse ? 0.09 : 0.04)).frame(width: 150, height: 225)
                    RoundedRectangle(cornerRadius: 3).fill(.white.opacity(0.06)).frame(width: 110, height: 10)
                }
            }
        }
        .onAppear { withAnimation(.easeInOut(duration: 0.9).repeatForever()) { pulse = true } }
    }
}

private struct HoverLift: ViewModifier {
    var cornerRadius: CGFloat
    @State private var hover = false

    func body(content: Content) -> some View {
        content
            .overlay(RoundedRectangle(cornerRadius: cornerRadius).stroke(.white.opacity(hover ? 0.35 : 0), lineWidth: 1))
            .brightness(hover ? 0.04 : 0)
            .scaleEffect(hover ? 1.04 : 1)
            .shadow(color: .black.opacity(hover ? 0.55 : 0), radius: hover ? 16 : 0, y: hover ? 10 : 0) // none at rest: a blur per card costs while scrolling
            .animation(.spring(response: 0.3, dampingFraction: 0.75), value: hover)
            .cardHover { hover = $0 }
    }
}

extension View {
    /// Lifts a card toward the pointer on hover, like the TV app.
    func hoverLift(cornerRadius: CGFloat = 10) -> some View { modifier(HoverLift(cornerRadius: cornerRadius)) }
}

/// The studios behind a title, as logo tiles that open each studio's page.
struct StudioTiles: View {
    let studios: [Studio]

    var body: some View {
        Shelf(title: "Production") {
            ForEach(studios) { s in
                NavigationLink(value: StudioRoute(id: s.id, name: s.name)) { StudioLogo(studio: s, width: 180) }
                    .buttonStyle(.plain)
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
            RoundedRectangle(cornerRadius: 10).fill(Color(white: 0.95))
            if let url = state.api?.imageURL(studio.logoPath, size: "w300") {
                AsyncImage(url: url) { $0.resizable().scaledToFit() } placeholder: { Color.clear }
                    .padding(width * 0.14)
            } else {
                Text(studio.name).font(.headline).foregroundStyle(.black).multilineTextAlignment(.center).padding(10)
            }
        }
        .frame(width: width, height: width / 2)
    }
}

struct StudioView: View {
    let route: StudioRoute

    var body: some View {
        StudioBody(route: route).ambientBackdrop()
    }
}

/// A studio's movies and shows over its artwork, each shelf loading more near its end.
private struct StudioBody: View {
    @Environment(AppState.self) private var state
    @Environment(\.ambient) private var ambient
    let route: StudioRoute
    @State private var company: Company?
    @State private var shelves: [StudioShelf] = []
    @State private var error: String?

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 30) {
                header
                if company != nil {
                    ForEach(shelves) { s in
                        Shelf(title: s.row.title) {
                            ForEach(s.items) { t in TitleCard(title: t).onAppear { s.appeared(t, api: state.api) } }
                        }
                    }
                    if shelves.isEmpty {
                        ContentUnavailableView("No movies or shows listed", systemImage: "film")
                    }
                } else if let error {
                    ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 60)
                }
            }
            .padding(.bottom, 28)
        }
        .navigationTitle(company?.name ?? route.name)
        .task(id: route.id) {
            do {
                if company == nil, let c = try await state.api?.company(route.id) {
                    shelves = c.rows.map { StudioShelf(row: $0, studio: c.id) }
                    company = c
                }
                ambient?.path = company?.backdropPath
            } catch is CancellationError {
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 24) {
            StudioLogo(studio: Studio(id: route.id, name: company?.name ?? route.name, logoPath: company?.logoPath), width: 240)
            VStack(alignment: .leading, spacing: 10) {
                Text("STUDIO").font(.caption.weight(.bold)).kerning(1.2).foregroundStyle(Color.wrenEmber)
                Text(company?.name ?? route.name).font(.system(size: 40, weight: .bold)).lineLimit(2)
                let facts = [company?.headquarters, company?.originCountry].compactMap { $0 }.filter { !$0.isEmpty }
                if !facts.isEmpty {
                    Text(facts.joined(separator: "  ·  ")).foregroundStyle(.secondary)
                }
                if let about = company?.description, !about.isEmpty {
                    Text(about).foregroundStyle(.white.opacity(0.85)).frame(maxWidth: 720, alignment: .leading).textSelection(.enabled)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 28)
        .padding(.top, 40)
    }
}
