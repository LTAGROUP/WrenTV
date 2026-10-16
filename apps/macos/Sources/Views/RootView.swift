import SwiftUI

struct RootView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        Group {
            if state.restoring {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if state.user == nil {
                ConnectView()
            } else {
                MainView()
            }
        }
        .background(Color.black.opacity(0.92))
    }
}

struct ConnectView: View {
    @Environment(AppState.self) private var state
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

    var body: some View {
        VStack(spacing: 22) {
            Image(nsImage: NSApp.applicationIconImage)
                .resizable()
                .frame(width: 84, height: 84)
            Text("Wren TV").font(.system(size: 34, weight: .bold))

            VStack(alignment: .leading, spacing: 12) {
                if !connected {
                    Text("Server").font(.headline)
                    TextField("localhost:32500", text: $address)
                        .textFieldStyle(.roundedBorder)
                        .onSubmit(connect)
                    Button(action: connect) {
                        Text("Connect").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(address.isEmpty || busy)
                } else if let server = state.server {
                    HStack {
                        Label(server.name, systemImage: "server.rack").font(.headline)
                        Spacer()
                        Button("Change") { connected = false; state.accessIssue = nil }.buttonStyle(.link)
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
            .frame(width: 320)
            .padding(24)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 14))
            if busy { ProgressView().controlSize(.small) }
            if !state.downloads.items.isEmpty {
                OpenDownloadsButton(count: state.downloads.items.filter { $0.status == .done }.count)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onAppear {
            if let url = state.serverURL {
                address = url.absoluteString
                connect()
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
            .buttonStyle(.link)
            Spacer()
        }
    }

    @ViewBuilder
    private func localForm(_ server: ServerInfo) -> some View {
        if server.setupRequired {
            Text("Create the owner account for this server.").foregroundStyle(.secondary)
        }
        TextField("Username", text: $username).textFieldStyle(.roundedBorder)
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
                Button("Open in Browser") { NSWorkspace.shared.open(url) }
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
                let ok = try await state.accountSignIn(email: email.trimmingCharacters(in: .whitespaces), password: password,
                                                       name: creating ? name.trimmingCharacters(in: .whitespaces) : nil)
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
    @Environment(\.openWindow) private var openWindow
    let count: Int

    var body: some View {
        Button { openWindow(id: "downloads") } label: {
            Label("Watch Downloads\(count > 0 ? " (\(count))" : "")", systemImage: "arrow.down.circle")
        }
        .buttonStyle(.link)
    }
}
