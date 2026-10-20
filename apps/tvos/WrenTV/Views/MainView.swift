import SwiftUI

/// The tabs across the top: Home, the libraries, Live TV, Search, Settings.
struct MainView: View {
    @Environment(AppState.self) private var state
    @State private var tab = "home"

    var body: some View {
        TabView(selection: $tab) {
            Tab("Home", systemImage: "house", value: "home") {
                TabStack { HomeView() }
            }
            if state.canStream {
                Tab("Discover", systemImage: "sparkles", value: "discover") {
                    TabStack { DiscoverView() }
                }
            }
            ForEach(libraryTabs) { lib in
                Tab(lib.name, systemImage: lib.symbol, value: "lib:" + lib.id) {
                    TabStack { LibraryView(library: lib) }
                }
            }
            if libraryTabs.isEmpty && state.libraries.count > 0 {
                Tab("Libraries", systemImage: "square.stack", value: "libraries") {
                    TabStack { LibrariesView() }
                }
            }
            if state.server?.liveTv == true {
                Tab("Live TV", systemImage: "dot.radiowaves.left.and.right", value: "live") {
                    TabStack { LiveTVView() }
                }
            }
            Tab(value: "search", role: .search) {
                TabStack { SearchView() }
            }
            Tab("Settings", systemImage: "gearshape", value: "settings") {
                TabStack { SettingsView() }
            }
        }
    }

    /// Each library gets a tab while they fit; more than four share one.
    private var libraryTabs: [Library] {
        state.libraries.count <= 4 ? state.libraries : []
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
                .navigationDestination(for: Library.self) { LibraryView(library: $0) }
                .navigationDestination(for: StreamTitleRoute.self) { StreamTitleView(route: $0) }
                .navigationDestination(for: WrenCollection.self) { CollectionView(collection: $0) }
                .navigationDestination(for: GenreRoute.self) { GenreView(route: $0) }
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
