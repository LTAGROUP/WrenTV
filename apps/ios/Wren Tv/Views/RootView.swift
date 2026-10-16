import SwiftUI

struct RootView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        @Bindable var state = state
        Group {
            if state.restoring {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if state.user == nil {
                NavigationStack { ConnectView() }
            } else {
                MainView()
            }
        }
        .background(Color.black)
        .fullScreenCover(item: $state.playing) { target in
            PlayerView(target: target)
                .environment(state)
                .preferredColorScheme(.dark)
        }
    }
}

struct ConnectView: View {
    @Environment(AppState.self) private var state
    @Environment(\.openURL) private var openURL
    @State private var address = ""
    @State private var username = ""
    @State private var email = ""
    @State private var name = ""
    @State private var password = ""
    @State private var creating = false
    @State private var notice: String?
    @State private var busy = false
    @State private var error: String?
    @State private var connected = false
    /// Connecting by typing a server's address instead of through an account.
    @State private var byAddress = false
    @State private var opening: String?

    var body: some View {
        ScrollView {
        VStack(spacing: 22) {
            Image("WrenLogo")
                .resizable()
                .frame(width: 84, height: 84)
                .padding(.top, 40)
            Text("Wren TV").font(.system(size: 34, weight: .bold))

            VStack(alignment: .leading, spacing: 12) {
                if state.account != nil && !byAddress {
                    serverPicker
                } else if !byAddress {
                    accountForm
                    Divider().padding(.vertical, 4)
                    Button { byAddress = true; error = nil } label: {
                        Label("Connect to a server by address", systemImage: "network").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderless)
                    .font(.callout)
                } else if !connected {
                    Text("Server").font(.headline)
                    TextField("192.168.1.10:32500", text: $address)
                        .textFieldStyle(.roundedBorder)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.go)
                        .onSubmit(connect)
                    Button(action: connect) {
                        Text("Connect").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(address.isEmpty || busy)
                    Button { byAddress = false; error = nil } label: {
                        Label(state.account != nil ? "Back to your servers" : "Sign in with a Wren TV account instead", systemImage: "person.crop.circle")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderless)
                    .font(.callout)
                } else if let server = state.server {
                    HStack {
                        Label(server.name, systemImage: "server.rack").font(.headline)
                        Spacer()
                        Button("Change") { connected = false; state.accessIssue = nil }.buttonStyle(.borderless)
                    }
                    if let issue = state.accessIssue {
                        accessIssueView(issue)
                    } else if server.usesAccounts {
                        accountForm
                    } else {
                        localForm(server)
                    }
                }
                if let error {
                    Text(error).foregroundStyle(.red).font(.callout)
                }
            }
            .frame(maxWidth: 380)
            .padding(24)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 14))
            .padding(.horizontal, 16)
            if busy { ProgressView().controlSize(.small) }
            if !state.downloads.items.isEmpty {
                OpenDownloadsButton(count: state.downloads.items.filter { $0.status == .done }.count)
            }
        }
        .frame(maxWidth: .infinity)
        }
        .scrollDismissesKeyboard(.interactively)
        .toolbar(.hidden, for: .navigationBar)
        .onAppear {
            if let url = state.serverURL, state.account == nil {
                byAddress = true
                address = url.absoluteString
                connect()
            }
        }
    }

    /// The account's servers, once signed in to a Wren TV account.
    @ViewBuilder
    private var serverPicker: some View {
        HStack {
            Label(state.accountEmail ?? "", systemImage: "person.crop.circle")
                .foregroundStyle(.secondary).lineLimit(1)
            Spacer()
            Button("Sign Out") { state.signOut() }.buttonStyle(.borderless)
        }
        .font(.callout)
        Text("Your servers").font(.headline).padding(.top, 4)
        if let servers = state.accountServers {
            if servers.isEmpty {
                Text("No servers yet. Install Wren TV on a computer and claim it with this account, or ask a server's owner to share it with \(state.accountEmail ?? "this email").")
                    .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            ForEach(servers) { s in
                Button { open(s) } label: { serverRow(s) }
                    .buttonStyle(.plain)
                    .disabled(opening != nil)
            }
        } else if let e = state.accountServersError {
            Text(e).font(.callout).foregroundStyle(.red)
        } else {
            ProgressView().frame(maxWidth: .infinity)
        }
        if let issue = state.accessIssue {
            Text(issue).font(.callout).fixedSize(horizontal: false, vertical: true)
        }
        HStack {
            Button {
                Task { await state.loadAccountServers() }
            } label: { Label("Refresh", systemImage: "arrow.clockwise") }
            Spacer()
            Button("Use an address") { byAddress = true; error = nil }
        }
        .buttonStyle(.borderless)
        .font(.callout)
        .padding(.top, 4)
    }

    private func serverRow(_ s: AccountServer) -> some View {
        HStack(spacing: 12) {
            Image(systemName: "server.rack").font(.title3).foregroundStyle(Color.wrenEmber).frame(width: 28)
            VStack(alignment: .leading, spacing: 2) {
                Text(s.name).font(.headline).lineLimit(1)
                Text(serverDetail(s)).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            if opening == s.id {
                ProgressView()
            } else {
                Image(systemName: "chevron.right").font(.caption.bold()).foregroundStyle(.tertiary)
            }
        }
        .padding(12)
        .background(.white.opacity(0.06), in: RoundedRectangle(cornerRadius: 10))
        .contentShape(Rectangle())
    }

    private func serverDetail(_ s: AccountServer) -> String {
        let whose = s.owner_id == state.account?.userId ? "Yours" : "Shared with you"
        guard let seen = s.lastSeen else { return whose + " · never online" }
        if Date().timeIntervalSince(seen) < 600 { return whose + " · online" }
        return whose + " · seen " + seen.formatted(.relative(presentation: .named))
    }

    private func open(_ s: AccountServer) {
        opening = s.id
        error = nil
        Task {
            defer { opening = nil }
            do {
                try await state.open(s)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    @ViewBuilder
    private var accountForm: some View {
        Text(creating ? "Create your Wren TV account." : "Sign in with your Wren TV account.").foregroundStyle(.secondary)
        if creating {
            TextField("Name", text: $name).textFieldStyle(.roundedBorder).textContentType(.name)
        }
        TextField("Email", text: $email).textFieldStyle(.roundedBorder).textContentType(.username)
            .keyboardType(.emailAddress).textInputAutocapitalization(.never).autocorrectionDisabled()
        SecureField("Password", text: $password).textFieldStyle(.roundedBorder)
            .textContentType(creating ? .newPassword : .password)
            .onSubmit(accountSignIn)
        Button(action: accountSignIn) {
            Text(creating ? "Create Account" : "Sign In").frame(maxWidth: .infinity)
        }
        .buttonStyle(.borderedProminent)
        .disabled(email.isEmpty || password.isEmpty || (creating && name.isEmpty) || busy)
        if let notice {
            Text(notice).font(.callout).foregroundStyle(.secondary)
        }
        HStack {
            Spacer()
            Button(creating ? "Already have an account? Sign in" : "New to Wren TV? Create an account") {
                creating.toggle()
                error = nil
                notice = nil
            }
            .buttonStyle(.borderless)
            .font(.callout)
            Spacer()
        }
    }

    @ViewBuilder
    private func localForm(_ server: ServerInfo) -> some View {
        if server.setupRequired {
            Text("Create the owner account for this server.").foregroundStyle(.secondary)
        }
        TextField("Username", text: $username).textFieldStyle(.roundedBorder)
            .textContentType(.username).textInputAutocapitalization(.never).autocorrectionDisabled()
        SecureField("Password", text: $password).textFieldStyle(.roundedBorder).onSubmit(signIn)
        Button(action: signIn) {
            Text(server.setupRequired ? "Create Account" : "Sign In").frame(maxWidth: .infinity)
        }
        .buttonStyle(.borderedProminent)
        .disabled(username.isEmpty || password.isEmpty || busy)
    }

    @ViewBuilder
    private func accessIssueView(_ issue: String) -> some View {
        if let email = state.accountEmail {
            Label(email, systemImage: "person.crop.circle").foregroundStyle(.secondary)
        }
        Text(issue).fixedSize(horizontal: false, vertical: true)
        HStack {
            Button("Check Again") {
                busy = true
                Task { await state.retryAccess(); busy = false }
            }
            .buttonStyle(.borderedProminent)
            if state.server?.setupRequired == true, let url = state.serverURL {
                Button("Open in Browser") { openURL(url) }
            }
            Spacer()
            Button("Sign Out") { state.signOut() }
        }
    }

    private func accountSignIn() {
        busy = true
        error = nil
        notice = nil
        Task {
            defer { busy = false }
            do {
                let e = email.trimmingCharacters(in: .whitespaces)
                let n = creating ? name.trimmingCharacters(in: .whitespaces) : nil
                // Signed in through a server typed in by address, or straight to the account.
                let ok = connected
                    ? try await state.accountSignIn(email: e, password: password, name: n)
                    : try await state.signInToAccount(email: e, password: password, name: n)
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

    private func connect() {
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do {
                _ = try await state.connect(address)
                connected = true
            } catch {
                self.error = "Couldn't reach server: \(error.localizedDescription)"
            }
        }
    }

    private func signIn() {
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do {
                try await state.login(username: username, password: password, createOwner: state.server?.setupRequired ?? false)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

/// Downloads play offline, so they're reachable before connecting.
private struct OpenDownloadsButton: View {
    let count: Int

    var body: some View {
        NavigationLink { DownloadsView() } label: {
            Label("Watch Downloads\(count > 0 ? " (\(count))" : "")", systemImage: "arrow.down.circle")
        }
        .buttonStyle(.borderless)
        .padding(.bottom, 24)
    }
}
