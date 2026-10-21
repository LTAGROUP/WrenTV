import SwiftUI

/// A TMDB title: links to the library item when the server has it. Without
/// it, streaming servers open it to play; others link to TMDB.
struct TitleCard: View {
    @Environment(AppState.self) private var state
    let title: TitleView
    var rank: Int?
    var width: CGFloat = 150

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
            Text(title.title).font(.callout).lineLimit(1)
            if let y = title.year { Text(String(y)).font(.caption).foregroundStyle(.secondary) }
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
            }
        }
    }

    private func initials(_ name: String) -> String {
        name.split(separator: " ").prefix(2).compactMap(\.first).map(String.init).joined()
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
