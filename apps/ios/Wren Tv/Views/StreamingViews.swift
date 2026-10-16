import SwiftUI

/// Opens a title the server doesn't have yet: adds it to the streaming
/// library, then shows it like any other item.
struct StreamTitleView: View {
    @Environment(AppState.self) private var state
    let route: StreamTitleRoute
    @State private var item: Item?
    @State private var error: String?

    var body: some View {
        Group {
            if let item {
                ItemDetailView(itemId: item.id, seed: item)
            } else if let error {
                ContentUnavailableView("Couldn't open \(route.title)", systemImage: "exclamationmark.triangle", description: Text(error))
            } else {
                ProgressView("Opening \(route.title)…").frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .task(id: route) {
            do {
                item = try await state.api?.addStreamTitle(tmdbId: route.tmdbId, mediaType: route.mediaType)
                await state.reloadLibraries()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

/// Admin actions for streamed titles.
struct StreamAdminMenu: View {
    @Environment(AppState.self) private var state
    let item: Item
    var onChange: () -> Void
    @State private var notice: String?
    @State private var confirmForget = false

    var body: some View {
        Menu {
            Button {
                Task {
                    do {
                        let n = try await state.api?.serverDownload(item.id) ?? 0
                        notice = n > 1 ? "\(n) episodes queued for download to the server." : "Queued for download to the server."
                    } catch {
                        notice = error.localizedDescription
                    }
                }
            } label: { Label("Download to Server", systemImage: "externaldrive.badge.plus") }
            Button { confirmForget = true } label: { Label("Find New Streams", systemImage: "arrow.triangle.2.circlepath") }
        } label: {
            Label("Stream", systemImage: "dot.radiowaves.left.and.right")
        }
        .controlSize(.large)
        .fixedSize()
        .help("Streamed through the server's debrid service")
        .alert(notice ?? "", isPresented: Binding(get: { notice != nil }, set: { if !$0 { notice = nil } })) {
            Button("OK") {}
        }
        .confirmationDialog("Find new streams?", isPresented: $confirmForget) {
            Button("Find New Streams") {
                Task {
                    try? await state.api?.forgetStreams(item.id)
                    onChange()
                }
            }
        } message: {
            Text("The streams saved for \(item.kind == "show" ? "every episode of this show" : item.kind == "season" ? "this season" : "this title") are forgotten. The next play searches the addons again.")
        }
    }
}
