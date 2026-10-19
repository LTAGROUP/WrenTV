import Sparkle
import SwiftUI

struct PlayTarget: Codable, Hashable {
    var itemId: String
    var title: String
    var subtitle: String?
    var fromStart = false
    /// A specific version (file) to play; nil lets the server choose.
    var fileId: String?
    /// A downloaded copy on this Mac, played without the server.
    var localFile: String?
    var localPosition: Double?
}

@main
struct WrenTVApp: App {
    @State private var state = AppState()
    /// Checks for a new version at launch; see SUFeedURL in project.yml.
    private let updater = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: nil, userDriverDelegate: nil)

    init() {
        URLCache.shared = URLCache(memoryCapacity: 96 << 20, diskCapacity: 1 << 30)
    }

    var body: some Scene {
        WindowGroup("Wren TV", id: "main") {
            RootView()
                .environment(state)
                .frame(minWidth: 980, minHeight: 620)
                .preferredColorScheme(.dark)
        }
        .defaultSize(width: 1320, height: 840)
        .commands {
            CommandGroup(after: .appInfo) {
                Button("Check for Updates…") { updater.checkForUpdates(nil) }
                if state.user != nil {
                    Button("Sign Out") { state.signOut() }
                }
            }
        }

        WindowGroup("Player", id: "player", for: PlayTarget.self) { $target in
            if let target {
                PlayerWindow(target: target)
                    .environment(state)
                    .preferredColorScheme(.dark)
            }
        }
        .windowStyle(.hiddenTitleBar)
        .defaultSize(width: 1280, height: 720)

        WindowGroup("Live TV", id: "live", for: LiveTarget.self) { $target in
            if let target {
                LivePlayerWindow(target: target)
                    .environment(state)
                    .preferredColorScheme(.dark)
            }
        }
        .windowStyle(.hiddenTitleBar)
        .defaultSize(width: 1280, height: 720)

        Window("Downloads", id: "downloads") {
            NavigationStack { DownloadsView() }
                .environment(state)
                .frame(minWidth: 720, minHeight: 480)
                .preferredColorScheme(.dark)
        }
        .defaultSize(width: 980, height: 680)
        .keyboardShortcut("j", modifiers: [.command, .option])

        Settings {
            PreferencesView()
                .environment(state)
                .preferredColorScheme(.dark)
        }
    }
}
