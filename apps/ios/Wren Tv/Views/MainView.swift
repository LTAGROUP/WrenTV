import SwiftUI

enum AppTab: Hashable {
    case home, movies, shows, library, liveTV, downloads, more
}

/// Tabs on iPhone, a sidebar on iPad.
struct MainView: View {
    @Environment(AppState.self) private var state
    @Environment(\.horizontalSizeClass) private var sizeClass
    @State private var tab: AppTab = .home
    @AppStorage(PosterSize.key) private var posterSize = PosterSize.medium.rawValue

    /// Live TV gets a tab unless the iPhone's bar is already full (Movies
    /// and TV Shows); then it's the first row under More.
    private var liveTab: Bool { state.hasLiveTV && (sizeClass != .compact || !state.canStream) }

    var body: some View {
        TabView(selection: $tab) {
            Tab("Home", systemImage: "house", value: AppTab.home) {
                TabStack { LayoutPageView(scope: "home", title: "Home", root: true) { HomeView() } }
            }
            if state.canStream {
                // Everything plays, so there are no libraries to pick from:
                // just movies and shows.
                Tab("Movies", systemImage: "film", value: AppTab.movies) {
                    TabStack { LayoutPageView(scope: "movies", title: "Movies", root: true, browseAll: CatalogRoute(type: "movie")) { CatalogView(type: "movie") } }
                }
                Tab("TV Shows", systemImage: "tv", value: AppTab.shows) {
                    TabStack { LayoutPageView(scope: "shows", title: "TV Shows", root: true, browseAll: CatalogRoute(type: "show")) { CatalogView(type: "show") } }
                }
            } else {
                Tab("Library", systemImage: "square.stack", value: AppTab.library) {
                    TabStack { LibrariesView() }
                }
            }
            if liveTab {
                Tab("Live TV", systemImage: "antenna.radiowaves.left.and.right", value: AppTab.liveTV) {
                    TabStack { LiveTVView() }
                }
            }
            // iPhone fits five tabs: with Movies and TV Shows, or Live TV, Downloads moves into More.
            if state.showsDownloads && !state.canStream && !(liveTab && sizeClass == .compact) {
                Tab("Downloads", systemImage: "arrow.down.circle", value: AppTab.downloads) {
                    TabStack { DownloadsView() }
                }
                .badge(state.downloads.items.filter(\.active).count)
            }
            Tab("More", systemImage: "ellipsis.circle", value: AppTab.more) {
                TabStack { MoreView() }
            }
        }
        .tabViewStyle(.sidebarAdaptable)
        .minimizesTabBarOnScroll()
        // Card sizes are read as pages draw, so a new size redraws them.
        .id(posterSize)
    }
}

/// A tab's navigation stack, with every page the app can push.
struct TabStack<Root: View>: View {
    @ViewBuilder var root: Root

    var body: some View {
        NavigationStack {
            root.wrenDestinations()
        }
    }
}

/// The More tab's pages. They're pushed as values like every other page,
/// since a stack that mixes value links with view links pops the view-linked
/// page as soon as something is pushed from it.
enum PageRoute: Hashable {
    case liveTV, watchlist, downloads, collections, calendar, preferences, linkTV
    case stats, libraries, users, serverDownloads, serverSettings

    @ViewBuilder var page: some View {
        switch self {
        case .liveTV: LiveTVView()
        case .watchlist: WatchlistView()
        case .downloads: DownloadsView()
        case .collections: CollectionsView()
        case .calendar: CalendarView()
        case .preferences: PreferencesView()
        case .linkTV: LinkTVView()
        case .stats: StatsView()
        case .libraries: ManageLibrariesView()
        case .users: UsersView()
        case .serverDownloads: ServerDownloadsView()
        case .serverSettings: ServerSettingsView()
        }
    }
}

extension View {
    func wrenDestinations() -> some View {
        navigationDestination(for: Item.self) { ItemDetailView(itemId: $0.id, seed: $0) }
            .navigationDestination(for: ItemIDRoute.self) { ItemDetailView(itemId: $0.id) }
            .navigationDestination(for: WrenCollection.self) { CollectionDetailView(seed: $0) }
            .navigationDestination(for: GenreRoute.self) { GenreItemsView(route: $0) }
            .navigationDestination(for: StreamTitleRoute.self) { StreamTitleView(route: $0) }
            .navigationDestination(for: PersonRoute.self) { PersonView(route: $0) }
            .navigationDestination(for: SearchRoute.self) { _ in SearchTab() }
            .navigationDestination(for: HubRoute.self) { HubView(route: $0) }
            .navigationDestination(for: LayoutEditorRoute.self) { LayoutEditorView(route: $0) }
            .navigationDestination(for: CatalogRoute.self) { CatalogView(type: $0.type) }
            .navigationDestination(for: Library.self) { LibraryView(library: $0) }
            .navigationDestination(for: LibraryPageRoute.self) { LibraryPage(library: $0.library) }
            .navigationDestination(for: LayoutsRoute.self) { _ in LayoutsView() }
            .navigationDestination(for: PageRoute.self) { $0.page }
    }
}

/// A library's own page: its layout rows, then the whole library under Browse All.
struct LibraryPage: View {
    let library: Library

    var body: some View {
        LayoutPageView(scope: "library:\(library.id)", title: library.name, browseAll: library) { LibraryView(library: library) }
            .id(library.id)
    }
}

/// Libraries and collections when the server doesn't stream.
struct LibrariesView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        List {
            Section {
                NavigationLink(value: PageRoute.watchlist) { Label("Watchlist", systemImage: "bookmark") }
                NavigationLink(value: PageRoute.collections) { Label("Collections", systemImage: "square.stack") }
                if state.libraries.contains(where: { $0.type == "shows" }) {
                    NavigationLink(value: PageRoute.calendar) { Label("Calendar", systemImage: "calendar") }
                }
            }
            Section("Libraries") {
                ForEach(state.libraries) { lib in
                    NavigationLink(value: LibraryPageRoute(library: lib)) { Label(lib.name, systemImage: lib.symbol) }
                }
            }
        }
        .navigationTitle("Library")
        .refreshable { await state.reloadLibraries() }
    }
}

/// Collections, your settings, the server's admin pages and the account.
struct MoreView: View {
    @Environment(AppState.self) private var state
    @Environment(\.horizontalSizeClass) private var sizeClass
    @State private var confirmSignOut = false

    var body: some View {
        List {
            if state.canStream || (state.hasLiveTV && sizeClass == .compact) {
                Section {
                    if state.hasLiveTV && state.canStream && sizeClass == .compact {
                        NavigationLink(value: PageRoute.liveTV) { Label("Live TV", systemImage: "antenna.radiowaves.left.and.right") }
                    }
                    if state.canStream {
                        NavigationLink(value: PageRoute.watchlist) { Label("Watchlist", systemImage: "bookmark") }
                    }
                    if state.showsDownloads && (state.canStream || (state.hasLiveTV && sizeClass == .compact)) {
                        NavigationLink(value: PageRoute.downloads) { Label("Downloads", systemImage: "arrow.down.circle") }
                            .badge(state.downloads.items.filter(\.active).count)
                    }
                    if state.canStream {
                        NavigationLink(value: PageRoute.collections) { Label("Collections", systemImage: "square.stack") }
                        NavigationLink(value: PageRoute.calendar) { Label("Calendar", systemImage: "calendar") }
                    }
                }
            }
            Section {
                NavigationLink(value: PageRoute.preferences) { Label("Settings", systemImage: "slider.horizontal.3") }
                NavigationLink(value: LayoutsRoute()) { Label("Layouts", systemImage: "rectangle.3.group") }
                NavigationLink(value: PageRoute.linkTV) { Label("Link a TV", systemImage: "tv.badge.wifi") }
            }
            if state.isAdmin {
                Section("Server") {
                    NavigationLink(value: PageRoute.stats) { Label("Stats", systemImage: "chart.bar") }
                    NavigationLink(value: PageRoute.libraries) { Label("Libraries", systemImage: "folder") }
                    NavigationLink(value: PageRoute.users) { Label("Users", systemImage: "person.2") }
                    if state.server?.streaming == true {
                        NavigationLink(value: PageRoute.serverDownloads) { Label("Server Downloads", systemImage: "externaldrive.badge.plus") }
                    }
                    NavigationLink(value: PageRoute.serverSettings) { Label("Settings", systemImage: "gearshape") }
                }
            }
            Section {
                LabeledContent {
                    Text(state.accountEmail ?? state.user?.displayName ?? "")
                } label: {
                    Label(state.user?.displayName ?? "Account", systemImage: "person.crop.circle")
                }
                if let server = state.server {
                    LabeledContent {
                        Text(state.api?.baseURL.host() ?? "")
                    } label: {
                        Label(server.name, systemImage: "server.rack")
                    }
                }
                if state.account != nil {
                    Button { state.switchServer() } label: {
                        Label("Switch Server", systemImage: "arrow.left.arrow.right")
                    }
                }
                Button(role: .destructive) { confirmSignOut = true } label: {
                    Label("Sign Out", systemImage: "rectangle.portrait.and.arrow.right")
                }
            }
        }
        .navigationTitle("More")
        .confirmationDialog(state.account != nil ? "Sign out of your Wren TV account?" : "Sign out of \(state.server?.name ?? "this server")?", isPresented: $confirmSignOut, titleVisibility: .visible) {
            Button("Sign Out", role: .destructive) { state.signOut() }
        } message: {
            Text("Downloads stay on this device.")
        }
    }
}

/// Opens the search page.
struct SearchRoute: Hashable {}

/// The search bar floating over the top of the home page. Tapping it opens
/// the search page with the keyboard up.
private struct HomeSearchBar: View {
    var body: some View {
        NavigationLink(value: SearchRoute()) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                Text("Search movies and shows")
                Spacer(minLength: 0)
            }
            .font(.body)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 14)
            .frame(maxWidth: 560, minHeight: 42)
            .searchCapsuleBackground()
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 16)
        .padding(.top, 4)
    }
}

private extension View {
    @ViewBuilder
    func searchCapsuleBackground() -> some View {
        if #available(iOS 26, *) {
            glassEffect(.regular.interactive(), in: Capsule())
        } else {
            background(.ultraThinMaterial, in: Capsule())
        }
    }
}

extension View {
    func homeSearchBar() -> some View {
        overlay(alignment: .top) { HomeSearchBar() }
    }
}

/// The search page: library matches first, then everything else TMDB knows.
struct SearchTab: View {
    @State private var query = ""
    @FocusState private var focused: Bool

    var body: some View {
        Group {
            if query.isEmpty {
                ContentUnavailableView("Search Wren TV", systemImage: "magnifyingglass",
                                       description: Text("Find movies and shows by title."))
            } else {
                SearchView(query: query)
            }
        }
        .navigationTitle("Search")
        .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Movies and shows")
        .searchFocused($focused)
        .onAppear { if query.isEmpty { focused = true } }
    }
}

extension AppState {
    /// The server has live TV channels.
    var hasLiveTV: Bool { server?.liveTv == true }

    /// Downloads exist on this device or this user may make them.
    var showsDownloads: Bool { !downloads.items.isEmpty || limits?.canDownload == true }
}
