import AVFoundation
import SwiftUI
import UIKit

@main
struct WrenTVApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @State private var state = AppState()

    init() {
        URLCache.shared = URLCache(memoryCapacity: 96 << 20, diskCapacity: 1 << 30)
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(state)
                .preferredColorScheme(.dark)
                .tint(Color.wrenEmber)
                .onAppear { appDelegate.state = state }
        }
    }
}

final class AppDelegate: NSObject, UIApplicationDelegate {
    weak var state: AppState?
    /// Kept until the downloads session reports its events, if the system
    /// relaunched the app for them before the UI came up.
    private var pendingCompletion: (() -> Void)?

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        // Video keeps its sound with the ring switch off and plays on in Picture in Picture.
        try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .moviePlayback)
        return true
    }

    func application(_ application: UIApplication, handleEventsForBackgroundURLSession identifier: String, completionHandler: @escaping () -> Void) {
        guard identifier == DownloadManager.sessionIdentifier else { return completionHandler() }
        if let state {
            state.downloads.backgroundCompletion = completionHandler
        } else {
            pendingCompletion = completionHandler
            Task { @MainActor in
                // The scene connects right after launch; hand it over then.
                for _ in 0..<50 where self.state == nil { try? await Task.sleep(for: .milliseconds(100)) }
                if let state = self.state {
                    state.downloads.backgroundCompletion = self.pendingCompletion
                } else {
                    self.pendingCompletion?()
                }
                self.pendingCompletion = nil
            }
        }
    }
}
