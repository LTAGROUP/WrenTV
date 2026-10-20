import AVFoundation
import SwiftUI

@main
struct WrenTVApp: App {
    @State private var state = AppState()

    init() {
        URLCache.shared = URLCache(memoryCapacity: 128 << 20, diskCapacity: 512 << 20)
        try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .moviePlayback)
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(state)
                .preferredColorScheme(.dark)
                .tint(Color.wrenEmber)
                .onOpenURL { state.openLink($0) }
        }
    }
}

extension AppState {
    /// wrentv://play/<id>: from another app or a shortcut.
    func openLink(_ url: URL) {
        guard url.scheme == "wrentv", url.host == "play", api != nil else { return }
        let id = url.lastPathComponent
        guard !id.isEmpty, id != "/" else { return }
        play(PlayTarget(itemId: id, title: ""))
    }
}
