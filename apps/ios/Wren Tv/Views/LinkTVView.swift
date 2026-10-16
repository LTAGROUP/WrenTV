import SwiftUI
import Vision
import VisionKit

/// Signs a TV in as this user. The TV shows a code and a QR code of
/// <server>/link?code=…; scan it or type the code.
struct LinkTVView: View {
    @Environment(AppState.self) private var state
    @State private var code = ""
    @State private var scanning = false
    @State private var pending: (code: String, device: String)?
    @State private var linked: String?
    @State private var error: String?
    @State private var busy = false

    private var canScan: Bool { DataScannerViewController.isSupported && DataScannerViewController.isAvailable }

    var body: some View {
        Form {
            if let linked {
                Section {
                    Label("\(linked) is signed in as \(state.user?.displayName ?? "you").", systemImage: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                    Button("Link Another TV") { self.linked = nil; code = "" }
                }
            } else {
                if canScan {
                    Section {
                        Button { scanning = true } label: { Label("Scan the Code on Your TV", systemImage: "qrcode.viewfinder") }
                    } footer: {
                        Text("On the TV, choose Sign in with your phone.")
                    }
                }
                Section {
                    TextField("ABCD-EFGH", text: $code)
                        .textInputAutocapitalization(.characters)
                        .autocorrectionDisabled()
                        .font(.title3.monospaced())
                        .onSubmit { lookUp(code) }
                    Button("Continue") { lookUp(code) }
                        .disabled(Self.normalize(code).count < 8 || busy)
                } header: {
                    Text("Or type the code")
                } footer: {
                    if let error { Text(error).foregroundStyle(.red) }
                }
            }
        }
        .navigationTitle("Link a TV")
        .sheet(isPresented: $scanning) {
            NavigationStack {
                QRScanner { text in
                    scanning = false
                    if let c = Self.code(from: text) { lookUp(c) } else { error = "That isn't a Wren TV sign-in code." }
                }
                .ignoresSafeArea()
                .navigationTitle("Scan the TV's Code")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { scanning = false } } }
            }
        }
        .alert("Sign in this TV?", isPresented: Binding(get: { pending != nil }, set: { if !$0 { pending = nil } }), presenting: pending) { p in
            Button("Sign In") { approve(p.code, device: p.device) }
            Button("Cancel", role: .cancel) {}
        } message: { p in
            Text("\(p.device) will be signed in to \(state.server?.name ?? "this server") as \(state.user?.displayName ?? "you").")
        }
    }

    /// Codes are 8 characters; the TV shows them as ABCD-EFGH.
    static func normalize(_ s: String) -> String {
        let alpha = Set("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")
        return String(s.uppercased().filter { alpha.contains($0) })
    }

    /// Takes the code from a scanned link. Only the code is used: the approval
    /// always goes to the server this app is signed in to, never to an address
    /// read from a QR code.
    static func code(from text: String) -> String? {
        if let c = URLComponents(string: text), c.path.hasSuffix("/link"),
           let v = c.queryItems?.first(where: { $0.name == "code" })?.value {
            let n = normalize(v)
            return n.count == 8 ? n : nil
        }
        let n = normalize(text)
        return n.count == 8 && text.count <= 12 ? n : nil
    }

    private func lookUp(_ raw: String) {
        let c = Self.normalize(raw)
        guard c.count == 8, let api = state.api else { return }
        error = nil
        busy = true
        Task {
            defer { busy = false }
            do {
                let info = try await api.pairInfo(c)
                if info.approved {
                    error = "This code was already used. Get a new one on the TV."
                } else {
                    pending = (c, info.device)
                }
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private func approve(_ c: String, device: String) {
        guard let api = state.api else { return }
        Task {
            do {
                try await api.approvePair(c)
                linked = device
                code = ""
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

/// A camera view that reports the first QR code it sees.
private struct QRScanner: UIViewControllerRepresentable {
    var found: (String) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let vc = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])],
                                           qualityLevel: .balanced, isHighlightingEnabled: true)
        vc.delegate = context.coordinator
        try? vc.startScanning()
        return vc
    }

    func updateUIViewController(_ vc: DataScannerViewController, context: Context) {}

    static func dismantleUIViewController(_ vc: DataScannerViewController, coordinator: Coordinator) {
        vc.stopScanning()
    }

    func makeCoordinator() -> Coordinator { Coordinator(found: found) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let found: (String) -> Void
        var done = false
        init(found: @escaping (String) -> Void) { self.found = found }

        func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            guard !done else { return }
            for case .barcode(let b) in items {
                if let s = b.payloadStringValue {
                    done = true
                    found(s)
                    return
                }
            }
        }
    }
}
