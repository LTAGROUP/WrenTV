import SwiftUI

/// The pages whose rows can be chosen.
struct LayoutsView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        Form {
            Section {
                NavigationLink(value: LayoutEditorRoute(scope: "home", title: "Home")) { Text("Home") }
                if state.canStream {
                    NavigationLink(value: LayoutEditorRoute(scope: "movies", title: "Movies")) { Text("Movies") }
                    NavigationLink(value: LayoutEditorRoute(scope: "shows", title: "TV Shows")) { Text("TV Shows") }
                }
            }
            if !state.libraries.isEmpty {
                Section("Libraries") {
                    ForEach(state.libraries) { lib in
                        NavigationLink(value: LayoutEditorRoute(scope: "library:\(lib.id)", title: lib.name)) { Text(lib.name) }
                    }
                }
            }
        }
        .padding(.horizontal, 300)
        .navigationTitle("Layouts")
    }
}

/// One page's rows: their order, which are hidden, and which more to add.
/// Admins also choose whether they're editing their own layout or everyone's default.
struct LayoutEditorView: View {
    @Environment(AppState.self) private var state
    let route: LayoutEditorRoute
    @State private var everyone = false
    @State private var config: LayoutConfig?
    @State private var error: String?
    @State private var adding = false

    var body: some View {
        Form {
            if state.isAdmin {
                Section {
                    Picker("Editing", selection: $everyone) {
                        Text("My layout").tag(false)
                        Text("Default for everyone").tag(true)
                    }
                }
            }
            if let c = config {
                Section {
                    ForEach(Array(c.entries.enumerated()), id: \.element.id) { i, e in
                        Menu {
                            if i > 0 {
                                Button { move(i, to: 0) } label: { Label("Move to Top", systemImage: "arrow.up.to.line") }
                                Button { move(i, to: i - 1) } label: { Label("Move Up", systemImage: "arrow.up") }
                            }
                            if i < c.entries.count - 1 {
                                Button { move(i, to: i + 1) } label: { Label("Move Down", systemImage: "arrow.down") }
                            }
                            Button { toggle(i) } label: {
                                Label(e.isHidden ? "Show" : "Hide", systemImage: e.isHidden ? "eye" : "eye.slash")
                            }
                            Button(role: .destructive) { remove(i) } label: { Label("Remove", systemImage: "trash") }
                        } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(e.title ?? e.ref).foregroundStyle(e.isHidden ? .secondary : .primary)
                                    if let d = e.detail { Text(d).font(.caption).foregroundStyle(.secondary) }
                                }
                                Spacer()
                                if e.isHidden { Text("Hidden").font(.caption).foregroundStyle(.secondary) }
                            }
                        }
                    }
                    if let options = available, !options.isEmpty {
                        Button { adding = true } label: { Label("Add a Row…", systemImage: "plus") }
                    }
                } header: {
                    Text("Rows")
                } footer: {
                    if c.custom != true {
                        Text(!everyone && c.hasDefault == true ? "Showing the server's default. Changing anything makes it your own."
                                                                : "Showing Wren TV's built-in layout.")
                    }
                }
                if c.custom == true {
                    Section {
                        Button(!everyone && c.hasDefault == true ? "Use the Default Layout" : "Back to the Built-in Layout", role: .destructive) { reset() }
                    }
                }
            } else if let error {
                Text(error).foregroundStyle(Color.wrenBad)
            } else {
                ProgressView()
            }
        }
        .padding(.horizontal, 300)
        .navigationTitle(route.title)
        .task(id: "\(route.scope)|\(everyone)") { await load() }
        .sheet(isPresented: $adding) {
            NavigationStack {
                List(available ?? []) { o in
                    Button {
                        adding = false
                        add(o)
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(o.title ?? o.ref)
                            if let d = o.detail { Text(d).font(.caption).foregroundStyle(.secondary) }
                        }
                    }
                }
                .navigationTitle("Add a Row")
            }
        }
    }

    /// Options not already on the page.
    private var available: [LayoutEntry]? {
        guard let c = config else { return nil }
        let have = Set(c.entries.map(\.id))
        return c.options?.filter { !have.contains($0.id) }
    }

    private func load() async {
        do {
            config = try await state.api?.layoutConfig(route.scope, everyone: everyone)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func move(_ i: Int, to j: Int) {
        guard var e = config?.entries else { return }
        e.insert(e.remove(at: i), at: j)
        save(e)
    }

    private func toggle(_ i: Int) {
        guard var e = config?.entries else { return }
        e[i].hidden = !e[i].isHidden
        save(e)
    }

    private func remove(_ i: Int) {
        guard var e = config?.entries else { return }
        e.remove(at: i)
        save(e)
    }

    private func add(_ o: LayoutEntry) {
        guard var e = config?.entries else { return }
        e.append(o)
        save(e)
    }

    private func save(_ entries: [LayoutEntry]) {
        config?.entries = entries
        config?.custom = true
        Task {
            do {
                try await state.api?.setLayout(route.scope, everyone: everyone, entries: entries)
                state.refreshTick += 1
            } catch {
                self.error = error.localizedDescription
                await load()
            }
        }
    }

    private func reset() {
        Task {
            do {
                try await state.api?.resetLayout(route.scope, everyone: everyone)
                state.refreshTick += 1
                await load()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}
