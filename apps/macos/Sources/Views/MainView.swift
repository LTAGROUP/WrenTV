import SwiftUI

enum SidebarItem: Hashable {
    case home, watchlist, liveTV, collections, movies, shows, layouts
    case library(String)
    case downloads
    case stats, serverDownloads, users, manageLibraries, settings
}

struct MainView: View {
    @Environment(AppState.self) private var state
    @State private var selection: SidebarItem? = .home
    @State private var path = NavigationPath()
    @State private var search = ""

    var body: some View {
        NavigationSplitView {
            List(selection: $selection) {
                Section {
                    Label("Home", systemImage: "house").tag(SidebarItem.home)
                    Label("Watchlist", systemImage: "bookmark").tag(SidebarItem.watchlist)
                    if state.server?.liveTv == true {
                        Label("Live TV", systemImage: "antenna.radiowaves.left.and.right").tag(SidebarItem.liveTV)
                    }
                    if state.canStream {
                        // Everything plays, so there are no libraries to pick from:
                        // just movies and shows.
                        Label("Movies", systemImage: "film").tag(SidebarItem.movies)
                        Label("TV Shows", systemImage: "tv").tag(SidebarItem.shows)
                        Label("Collections", systemImage: "square.stack").tag(SidebarItem.collections)
                    } else {
                        Label("Collections", systemImage: "square.stack").tag(SidebarItem.collections)
                    }
                }
                if !state.canStream {
                    Section("Libraries") {
                        ForEach(state.libraries) { lib in
                            Label(lib.name, systemImage: lib.symbol).tag(SidebarItem.library(lib.id))
                        }
                    }
                }
                Section("This Mac") {
                    Label("Layouts", systemImage: "rectangle.3.group").tag(SidebarItem.layouts)
                    if !state.downloads.items.isEmpty || state.limits?.canDownload == true {
                        Label("Downloads", systemImage: "arrow.down.circle")
                            .badge(state.downloads.items.filter(\.active).count)
                            .tag(SidebarItem.downloads)
                    }
                }
                if state.isAdmin {
                    Section("Server") {
                        Label("Stats", systemImage: "chart.bar").tag(SidebarItem.stats)
                        Label("Libraries", systemImage: "folder").tag(SidebarItem.manageLibraries)
                        Label("Users", systemImage: "person.2").tag(SidebarItem.users)
                        if state.server?.streaming == true {
                            Label("Server Downloads", systemImage: "externaldrive.badge.plus").tag(SidebarItem.serverDownloads)
                        }
                        Label("Settings", systemImage: "gearshape").tag(SidebarItem.settings)
                    }
                }
            }
            .navigationSplitViewColumnWidth(min: 190, ideal: 210)
            .safeAreaInset(edge: .bottom) {
                HStack {
                    Image(systemName: "person.crop.circle")
                    Text(state.user?.displayName ?? "").lineLimit(1)
                    Spacer()
                    SettingsLink { Image(systemName: "gearshape") }
                        .buttonStyle(.borderless)
                        .help("Your Settings")
                    if state.account != nil {
                        Button { state.switchServer() } label: { Image(systemName: "arrow.left.arrow.right") }
                            .buttonStyle(.borderless)
                            .help("Switch Server")
                    }
                    Button { state.signOut() } label: { Image(systemName: "rectangle.portrait.and.arrow.right") }
                        .buttonStyle(.borderless)
                        .help("Sign Out")
                }
                .font(.callout)
                .foregroundStyle(.secondary)
                .padding(12)
            }
        } detail: {
            NavigationStack(path: $path) {
                content
                    .searchBar($search)
                    .navigationDestination(for: Item.self) { ItemDetailView(itemId: $0.id, seed: $0).searchBar($search) }
                    .navigationDestination(for: ItemIDRoute.self) { ItemDetailView(itemId: $0.id).searchBar($search) }
                    .navigationDestination(for: WrenCollection.self) { CollectionDetailView(seed: $0).searchBar($search) }
                    .navigationDestination(for: GenreRoute.self) { GenreItemsView(route: $0).searchBar($search) }
                    .navigationDestination(for: StreamTitleRoute.self) { StreamTitleView(route: $0).searchBar($search) }
                    .navigationDestination(for: HubRoute.self) { HubView(route: $0).searchBar($search) }
                    .navigationDestination(for: LayoutEditorRoute.self) { LayoutEditorView(route: $0).searchBar($search) }
                    .navigationDestination(for: Library.self) { LibraryView(library: $0).searchBar($search) }
                    .navigationDestination(for: CatalogRoute.self) { CatalogView(type: $0.type).searchBar($search) }
            }
        }
        .onChange(of: selection) {
            path = NavigationPath()
            search = ""
        }
        .onChange(of: search) { if !search.isEmpty { path = NavigationPath() } }
    }

    @ViewBuilder
    private var content: some View {
        if !search.isEmpty {
            SearchView(query: search)
        } else {
            page
        }
    }

    @ViewBuilder
    private var page: some View {
        switch selection ?? .home {
        case .home: LayoutPageView(scope: "home", title: "Home") { HomeView() }
        case .watchlist: WatchlistView()
        case .liveTV: LiveTVView()
        case .collections: CollectionsView()
        case .movies:
            LayoutPageView(scope: "movies", title: "Movies", label: "Movies", browseAll: CatalogRoute(type: "movie")) { CatalogView(type: "movie") }
        case .shows:
            LayoutPageView(scope: "shows", title: "TV Shows", label: "TV Shows", browseAll: CatalogRoute(type: "show")) { CatalogView(type: "show") }
        case .layouts: LayoutsView()
        case .stats: StatsView()
        case .library(let id):
            if let lib = state.libraries.first(where: { $0.id == id }) {
                LayoutPageView(scope: "library:\(lib.id)", title: lib.name, label: lib.name, browseAll: lib) { LibraryView(library: lib) }
                    .id(lib.id)
            }
        case .downloads: DownloadsView()
        case .serverDownloads: ServerDownloadsView()
        case .users: UsersView()
        case .manageLibraries: ManageLibrariesView()
        case .settings: ServerSettingsView()
        }
    }
}

/// The search field centered at the top of every page.
struct ToolbarSearchField: View {
    @Binding var text: String
    @FocusState private var focused: Bool

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField("Search movies and shows", text: $text)
                .textFieldStyle(.plain)
                .focused($focused)
                .onExitCommand { text = ""; focused = false }
            if !text.isEmpty {
                Button { text = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary) }
                    .buttonStyle(.plain)
                    .help("Clear")
            }
        }
        .font(.callout)
        .padding(.horizontal, 10)
        .frame(width: 340, height: 28)
        .modifier(SearchFieldBackground())
        .background {
            // ⌘F jumps to search from anywhere.
            Button("") { focused = true }.keyboardShortcut("f").hidden()
        }
    }
}

/// macOS 26 already puts toolbar items in a glass capsule; older systems
/// need one drawn.
private struct SearchFieldBackground: ViewModifier {
    func body(content: Content) -> some View {
        if #available(macOS 26, *) {
            content
        } else {
            content
                .background(.regularMaterial, in: Capsule())
                .overlay(Capsule().strokeBorder(.white.opacity(0.1)))
        }
    }
}

extension View {
    /// One search field, centered in the toolbar on every page, pushed
    /// pages included.
    func searchBar(_ text: Binding<String>) -> some View {
        toolbar {
            ToolbarItem(placement: .principal) { ToolbarSearchField(text: text) }
        }
    }
}
