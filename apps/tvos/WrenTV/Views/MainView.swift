import SwiftUI

/// The tabs across the top, as on every Wren TV: Home, Live TV, then Movies,
/// TV Shows and Collections when the server streams (each library and
/// Collections when it doesn't), Search and Settings.
struct MainView: View {
    @Environment(AppState.self) private var state
    @State private var tab = "home"
    @AppStorage(PosterSize.key) private var posterSize = PosterSize.medium.rawValue

    var body: some View {
        TabView(selection: $tab) {
            Tab("Home", systemImage: "house", value: "home") {
                TabStack { LayoutPageView(scope: "home") { HomeView() } }
            }
            if state.server?.liveTv == true {
                Tab("Live TV", systemImage: "dot.radiowaves.left.and.right", value: "live") {
                    TabStack { LiveTVView() }
                }
            }
            if state.canStream {
                Tab("Movies", systemImage: "film", value: "movies") {
                    TabStack { LayoutPageView(scope: "movies", label: "Movies") { DiscoverView() } }
                }
                Tab("TV Shows", systemImage: "tv", value: "shows") {
                    TabStack { LayoutPageView(scope: "shows", label: "TV Shows") { DiscoverView() } }
                }
            } else {
                ForEach(libraryTabs) { lib in
                    Tab(lib.name, systemImage: lib.symbol, value: "lib:" + lib.id) {
                        TabStack { LibraryPage(library: lib) }
                    }
                }
                if libraryTabs.isEmpty && state.libraries.count > 0 {
                    Tab("Libraries", systemImage: "square.stack", value: "libraries") {
                        TabStack { LibrariesView() }
                    }
                }
            }
            Tab("Collections", systemImage: "rectangle.stack", value: "collections") {
                TabStack { CollectionsView() }
            }
            if state.canStream || state.libraries.contains(where: { $0.type == "shows" }) {
                Tab("Calendar", systemImage: "calendar", value: "calendar") {
                    TabStack { CalendarView() }
                }
            }
            Tab(value: "search", role: .search) {
                TabStack { SearchView() }
            }
            Tab("Settings", systemImage: "gearshape", value: "settings") {
                TabStack { SettingsView() }
            }
        }
        // Card sizes are read as pages draw, so a new size redraws them.
        .id(posterSize)
    }

    /// Each library gets a tab while they fit; more than four share one.
    private var libraryTabs: [Library] {
        state.libraries.count <= 4 ? state.libraries : []
    }
}

/// A library's layout rows, or its grid on servers from before layouts.
struct LibraryPage: View {
    let library: Library

    var body: some View {
        LayoutPageView(scope: "library:\(library.id)", label: library.name) { LibraryView(library: library) }
    }
}

/// A tab's own navigation, with the pages it can open.
struct TabStack<Root: View>: View {
    @State private var router = Router()
    @ViewBuilder var root: () -> Root

    var body: some View {
        NavigationStack(path: $router.path) {
            root()
                .navigationDestination(for: ItemRoute.self) { DetailView(route: $0) }
                .navigationDestination(for: Library.self) { LibraryPage(library: $0) }
                .navigationDestination(for: StreamTitleRoute.self) { StreamTitleView(route: $0) }
                .navigationDestination(for: WrenCollection.self) { CollectionView(collection: $0) }
                .navigationDestination(for: GenreRoute.self) { GenreView(route: $0) }
                .navigationDestination(for: HubRoute.self) { HubView(route: $0) }
                .navigationDestination(for: LayoutEditorRoute.self) { LayoutEditorView(route: $0) }
                .navigationDestination(for: LayoutsRoute.self) { _ in LayoutsView() }
        }
        .environment(router)
    }
}

/// Every library, when there are too many for a tab each.
struct LibrariesView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        ScrollView {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 400), spacing: 48)], spacing: 48) {
                ForEach(state.libraries) { lib in
                    NavigationLink(value: lib) {
                        VStack(spacing: 18) {
                            Image(systemName: lib.symbol).font(.system(size: 60)).foregroundStyle(Color.wrenEmber)
                            Text(lib.name).font(.headline)
                            if let n = lib.itemCount { Text("\(n) titles").font(.caption).foregroundStyle(.secondary) }
                        }
                        .frame(maxWidth: .infinity, minHeight: 240)
                    }
                    .buttonStyle(.card)
                }
            }
            .padding(TV.gutter)
        }
    }
}
