import SwiftUI

/// Signed in: the app. Otherwise the sign-in screen. The players cover both.
struct RootView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        @Bindable var state = state
        ZStack {
            WrenBackground()
            if state.restoring {
                ProgressView()
            } else if state.user == nil || state.api == nil {
                ConnectView()
            } else {
                MainView()
            }
        }
        .fullScreenCover(item: $state.playing) { target in
            PlayerScreen(target: target)
                .environment(state)
        }
        .fullScreenCover(item: $state.playingLive) { target in
            LivePlayerScreen(target: target)
                .environment(state)
        }
    }
}
