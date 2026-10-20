import SwiftUI

/// Signing in. Account first: the email and password on the left and a QR
/// code to sign in from a phone on the right. The account's servers open by
/// themselves; typing a server's address is the fallback.
struct ConnectView: View {
    @Environment(AppState.self) private var state
    @State private var byAddress = false

    var body: some View {
        Group {
            if state.account != nil, let server = state.server, state.serverURL != nil {
                Page { AccountServerView(server: server) }
            } else if state.account != nil {
                Page { ServerPicker(useAddress: { byAddress = true }) }
            } else if byAddress {
                ByAddressView(back: { byAddress = false })
            } else {
                Page(side: { AccountLinkCard() }) {
                    AccountForm(title: "Sign in to Wren TV") { email, password, name in
                        try await state.signInToAccount(email: email, password: password, name: name)
                    }
                    Button { byAddress = true } label: { Label("Connect by Address", systemImage: "server.rack") }
                        .padding(.top, 30)
                }
            }
        }
        .onAppear {
            // A remembered address without an account is a server with its own usernames.
            if state.account == nil, state.serverURL != nil { byAddress = true }
        }
    }
}

/// The logo and a column of content, with an optional card on the right.
private struct Page<Content: View, Side: View>: View {
    var side: (() -> Side)?
    @ViewBuilder var content: () -> Content

    init(side: (() -> Side)? = nil, @ViewBuilder content: @escaping () -> Content) where Side == EmptyView {
        self.side = nil
        self.content = content
    }

    init(side: @escaping () -> Side, @ViewBuilder content: @escaping () -> Content) {
        self.side = side
        self.content = content
    }

    var body: some View {
        HStack(alignment: .center, spacing: 100) {
            VStack(alignment: .leading, spacing: 0) {
                Image("Lockup").resizable().scaledToFit().frame(height: 72)
                    .padding(.bottom, 60)
                content()
                Spacer(minLength: 0)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if let side { side() }
        }
        .padding(.horizontal, 120)
        .padding(.vertical, 80)
    }
}

/// Sign in from a phone without knowing any server: scan the code and approve.
private struct AccountLinkCard: View {
    @Environment(AppState.self) private var state
    @State private var link: AccountClient.DeviceLink?
    @State private var attempt = 0

    var body: some View {
        LinkCard(code: link?.code, link: link.map { "\(Self.page)?code=\($0.code)" },
                 hint: "Scan with your phone's camera or the Wren TV app, or go to \(Self.page.replacingOccurrences(of: "https://", with: "")) and enter the code.")
            .task(id: attempt) { await run() }
    }

    /// Where a phone approves a TV's account sign-in code.
    static let page = "https://watch.wrentv.com/link"

    private func run() async {
        link = nil
        guard let l = try? await state.startAccountLink() else {
            try? await Task.sleep(for: .seconds(5))
            if !Task.isCancelled { attempt += 1 }
            return
        }
        link = l
        let deadline = Date().addingTimeInterval(10 * 60 - 5)
        while Date() < deadline, !Task.isCancelled {
            try? await Task.sleep(for: .seconds(2))
            do {
                if try await state.pollAccountLink(l) { return }
            } catch let e as WrenTVError where e.status == 404 {
                break // expired
            } catch {
                // A network blip: keep waiting.
            }
        }
        if !Task.isCancelled { attempt += 1 } // a fresh code
    }
}

/// The QR code and the code to type, in a card.
private struct LinkCard: View {
    let code: String?
    let link: String?
    let hint: String

    var body: some View {
        VStack(spacing: 26) {
            Label("Sign in with your phone", systemImage: "iphone")
                .font(.title3.bold())
                .labelStyle(TintedIconLabel())
                .lineLimit(1)
                .minimumScaleFactor(0.8)
            if let code, let link {
                QRCode(text: link)
                    .padding(18)
                    .background(.white, in: RoundedRectangle(cornerRadius: 18))
                    .frame(width: 340, height: 340)
                Text(Self.grouped(code))
                    .font(.system(size: 52, weight: .bold, design: .monospaced))
                    .tracking(6)
                Text(hint)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                ProgressView().frame(width: 340, height: 340)
            }
        }
        .padding(44)
        .frame(width: 640)
        .background(Color.wrenSurface, in: RoundedRectangle(cornerRadius: 36))
    }

    /// "ABCD-EFGH"
    static func grouped(_ code: String) -> String {
        stride(from: 0, to: code.count, by: 4).map { i -> String in
            let s = code.index(code.startIndex, offsetBy: i)
            return String(code[s..<code.index(s, offsetBy: min(4, code.count - i))])
        }.joined(separator: "-")
    }
}

private struct TintedIconLabel: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 14) {
            configuration.icon.foregroundStyle(Color.wrenEmber)
            configuration.title
        }
    }
}

/// Email and password, or a new account's name too.
private struct AccountForm: View {
    let title: String
    let signIn: (String, String, String?) async throws -> Bool

    @State private var creating = false
    @State private var name = ""
    @State private var email = ""
    @State private var password = ""
    @State private var busy = false
    @State private var error: String?
    @State private var notice: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 22) {
            Text(creating ? "Create your Wren TV account" : title).font(.title2.bold())
            if creating {
                TextField("Name", text: $name).textContentType(.name)
            }
            TextField("Email", text: $email)
                .textContentType(.username)
                .keyboardType(.emailAddress)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
            SecureField("Password", text: $password)
                .textContentType(creating ? .newPassword : .password)
                .onSubmit { if ready { submit() } }
            HStack(spacing: 24) {
                Button(creating ? "Create Account" : "Sign In") { submit() }
                    .disabled(!ready)
                Button(creating ? "I have an account" : "Create an account") {
                    creating.toggle()
                    error = nil
                    notice = nil
                }
                if busy { ProgressView() }
            }
            if let notice { Text(notice).foregroundStyle(.secondary) }
            if let error { Text(error).foregroundStyle(Color.wrenBad) }
        }
        .frame(maxWidth: 820, alignment: .leading)
    }

    private var ready: Bool {
        !busy && !email.trimmingCharacters(in: .whitespaces).isEmpty && !password.isEmpty
            && (!creating || !name.trimmingCharacters(in: .whitespaces).isEmpty)
    }

    private func submit() {
        busy = true
        error = nil
        notice = nil
        Task {
            defer { busy = false }
            do {
                let ok = try await signIn(email.trimmingCharacters(in: .whitespaces), password,
                                          creating ? name.trimmingCharacters(in: .whitespaces) : nil)
                if !ok {
                    notice = "Check your email to confirm your account, then sign in."
                    creating = false
                }
                password = ""
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

/// Signed in to the account: its servers.
private struct ServerPicker: View {
    @Environment(AppState.self) private var state
    let useAddress: () -> Void
    @State private var opening: String?
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 26) {
            HStack(spacing: 24) {
                Text(state.accountEmail ?? "").font(.headline).foregroundStyle(.secondary).lineLimit(1)
                Button("Sign Out") { state.signOut() }
            }
            Text("Your servers").font(.title2.bold())
            if let servers = state.accountServers, !servers.isEmpty {
                VStack(spacing: 16) {
                    ForEach(servers) { s in
                        Button { open(s) } label: {
                            HStack(spacing: 24) {
                                Image(systemName: "server.rack").foregroundStyle(Color.wrenEmber)
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(s.name).font(.headline)
                                    Text(detail(s)).font(.callout).foregroundStyle(.secondary)
                                }
                                Spacer()
                                if opening == s.id { ProgressView() }
                            }
                            .padding(.vertical, 6)
                        }
                    }
                }
                .frame(maxWidth: 1000)
            } else if state.accountServers == nil && state.accountServersError == nil {
                ProgressView()
            } else {
                Text(state.accountServersError ?? "No servers yet. Install Wren TV on a computer and claim it with this account, or ask a server's owner to share it with \(state.accountEmail ?? "this email").")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: 1000, alignment: .leading)
            }
            if let error { Text(error).foregroundStyle(Color.wrenBad) }
            HStack(spacing: 24) {
                Button("Refresh") { Task { await state.loadAccountServers() } }
                Button("Connect by Address", action: useAddress)
            }
        }
        .task {
            if state.accountServers == nil { await state.loadAccountServers() }
        }
    }

    private func detail(_ s: AccountServer) -> String {
        let whose = s.owner_id == state.account?.userId ? "Yours" : "Shared with you"
        guard let seen = s.lastSeen else { return whose }
        let online = Date().timeIntervalSince(seen) < 180
        return whose + (online ? " · online" : " · offline")
    }

    private func open(_ s: AccountServer) {
        guard opening == nil else { return }
        opening = s.id
        error = nil
        Task {
            defer { opening = nil }
            do { try await state.open(s) } catch { self.error = error.localizedDescription }
        }
    }
}

/// Opened one of the account's servers: connecting, or why it can't be used yet.
private struct AccountServerView: View {
    @Environment(AppState.self) private var state
    let server: ServerInfo

    var body: some View {
        VStack(alignment: .leading, spacing: 30) {
            Label(server.name, systemImage: "server.rack").font(.title3)
            if let issue = state.accessIssue {
                AccessIssueView(issue: issue, change: { state.switchServer() })
            } else {
                ProgressView()
            }
        }
    }
}

private struct AccessIssueView: View {
    @Environment(AppState.self) private var state
    let issue: String
    var change: (() -> Void)?
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            if let email = state.accountEmail { Text(email).foregroundStyle(.secondary) }
            Text(issue).font(.title3).frame(maxWidth: 1100, alignment: .leading)
            HStack(spacing: 24) {
                Button("Check Again") {
                    busy = true
                    Task { await state.retryAccess(); busy = false }
                }
                if let change { Button("Other Servers", action: change) }
                Button("Sign Out") { state.signOut() }
                if busy { ProgressView() }
            }
        }
    }
}

/// Connecting by typing a server's address (servers with their own
/// usernames, or without internet).
private struct ByAddressView: View {
    @Environment(AppState.self) private var state
    let back: () -> Void
    @State private var address = ""
    @State private var connected = false
    /// Signing in to an account server with a local username instead.
    @State private var useUsername = false
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        let server = state.server
        // Approving from a phone needs someone already signed in on the server.
        let pairing = connected && server != nil && state.accessIssue == nil && !(server?.needsOwner ?? true)
            && (server?.usesAccounts == false || server?.localAccounts == true)
        Group {
            if pairing {
                Page(side: { PairingCard() }) { content(server) }
            } else {
                Page { content(server) }
            }
        }
        .onAppear {
            if address.isEmpty, let url = state.serverURL {
                address = url.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
                connect()
            }
        }
    }

    @ViewBuilder private func content(_ server: ServerInfo?) -> some View {
        VStack(alignment: .leading, spacing: 24) {
            if !connected || server == nil {
                Text("Connect to a server").font(.title.bold())
                Text("Enter the address of a Wren TV server, like 192.168.1.20 or wrentv.example.com.")
                    .foregroundStyle(.secondary)
                TextField("Server address", text: $address)
                    .keyboardType(.URL)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .onSubmit { if !address.isEmpty { connect() } }
                    .frame(maxWidth: 820)
                HStack(spacing: 24) {
                    Button("Connect") { connect() }.disabled(address.trimmingCharacters(in: .whitespaces).isEmpty || busy)
                    Button("Sign in with an Account", action: back)
                    if busy { ProgressView() }
                }
            } else if let server {
                HStack(spacing: 24) {
                    Label(server.name, systemImage: "server.rack").font(.title3)
                    Button("Change Server") {
                        connected = false
                        state.accessIssue = nil
                    }
                }
                if let issue = state.accessIssue {
                    AccessIssueView(issue: issue)
                } else if server.usesAccounts && !useUsername {
                    AccountForm(title: "Sign in with your Wren TV account") { email, password, name in
                        try await state.accountSignIn(email: email, password: password, name: name)
                    }
                    if server.localAccounts == true {
                        Button("Sign in with a Username Instead") { useUsername = true }
                    }
                } else {
                    LocalForm(setup: server.needsOwner)
                    if server.usesAccounts {
                        Button("Sign in with a Wren TV Account Instead") { useUsername = false }
                    }
                }
            }
            if let error { Text(error).foregroundStyle(Color.wrenBad) }
        }
    }

    private func connect() {
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do {
                _ = try await state.connect(address)
                connected = true
            } catch {
                self.error = "Couldn't reach the server: \(error.localizedDescription)"
            }
        }
    }
}

/// Sign in from a phone already signed in to this server.
private struct PairingCard: View {
    @Environment(AppState.self) private var state
    @State private var pair: PairStart?
    @State private var unsupported = false
    @State private var attempt = 0

    var body: some View {
        Group {
            if !unsupported {
                let link = pair.flatMap { state.pairingLink($0) }
                LinkCard(code: pair?.code, link: link?.absoluteString,
                         hint: "Scan with your phone's camera or the Wren TV app, or open \(link.map { "\($0.scheme ?? "http")://\($0.host ?? "")\($0.port.map { ":\($0)" } ?? "")/link" } ?? "the link") and enter the code.")
            }
        }
        .task(id: attempt) { await run() }
    }

    private func run() async {
        pair = nil
        let p: PairStart
        do {
            p = try await state.startPairing()
        } catch let e as WrenTVError where e.status == 404 || e.status == 405 {
            unsupported = true // a server from before TV sign-in codes
            return
        } catch {
            try? await Task.sleep(for: .seconds(5))
            if !Task.isCancelled { attempt += 1 }
            return
        }
        pair = p
        let deadline = Date().addingTimeInterval(Double(p.expiresIn ?? 600) - 5)
        while Date() < deadline, !Task.isCancelled {
            try? await Task.sleep(for: .seconds(2))
            do {
                if try await state.pollPairing(p) { return }
            } catch let e as WrenTVError where e.status == 404 {
                break
            } catch {}
        }
        if !Task.isCancelled { attempt += 1 }
    }
}

/// Servers with their own usernames.
private struct LocalForm: View {
    @Environment(AppState.self) private var state
    let setup: Bool
    @State private var username = ""
    @State private var password = ""
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 22) {
            Text(setup ? "Create the owner account for this server" : "Sign in").font(.title2.bold())
            TextField("Username", text: $username)
                .textContentType(.username)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
            SecureField("Password", text: $password)
                .textContentType(setup ? .newPassword : .password)
            HStack(spacing: 24) {
                Button(setup ? "Create Account" : "Sign In") { submit() }
                    .disabled(busy || username.isEmpty || password.isEmpty)
                if busy { ProgressView() }
            }
            if let error { Text(error).foregroundStyle(Color.wrenBad) }
        }
        .frame(maxWidth: 820, alignment: .leading)
    }

    private func submit() {
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do {
                try await state.login(username: username.trimmingCharacters(in: .whitespaces), password: password, createOwner: setup)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}
