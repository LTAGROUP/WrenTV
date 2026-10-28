import AVKit
import SwiftUI

/// The full-screen live player.
struct LivePlayerView: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let target: LiveTarget

    @State private var model: LivePlayerModel?
    @State private var controlsVisible = true
    @State private var hideTask: Task<Void, Never>?
    @State private var pip: AVPictureInPictureController?
    @State private var fill = false
    @State private var showList = false
    @State private var now = Date()

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let model {
                content(model)
            } else {
                ProgressView().controlSize(.large).tint(.white)
            }
        }
        .statusBarHidden(!controlsVisible)
        .persistentSystemOverlays(.hidden)
        .task {
            guard model == nil, let api = state.api else { return }
            var channels = state.liveChannels
            if channels.isEmpty { channels = (try? await api.liveChannels()) ?? [] }
            state.liveChannels = channels
            guard let ch = channels.first(where: { $0.id == target.channelId }) else { dismiss(); return }
            let list = target.channelIds.map { ids in
                // By id, not a search per channel: a big provider's guide has thousands.
                let byId = Dictionary(channels.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
                return ids.compactMap { byId[$0] }
            }
                ?? target.group.map { g in channels.filter { $0.group == g } } ?? channels
            let m = LivePlayerModel(api: api, channel: ch, channels: list)
            model = m
            m.tune(ch)
            poke()
        }
        .task {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                now = Date()
            }
        }
        .onDisappear { model?.stop() }
        .sheet(isPresented: $showList) {
            if let model {
                ChannelPicker(model: model, now: now) { showList = false }
                    .presentationDetents([.medium, .large])
            }
        }
    }

    @ViewBuilder
    private func content(_ model: LivePlayerModel) -> some View {
        ZStack {
            VideoLayerView(player: model.player, fill: fill, pip: $pip)
                .ignoresSafeArea()
                .onTapGesture(count: 2) { withAnimation { fill.toggle() } }
                .onTapGesture { toggleControls() }
                .gesture(DragGesture(minimumDistance: 40).onEnded { v in
                    // Swipe up for the next channel, down for the one before.
                    guard abs(v.translation.height) > abs(v.translation.width) else { return }
                    model.step(v.translation.height < 0 ? 1 : -1)
                    poke()
                })
            switch model.phase {
            case .loading:
                VStack(spacing: 12) {
                    ProgressView().controlSize(.large).tint(.white)
                    Text("Tuning to \(model.channel.name)…").foregroundStyle(.white.opacity(0.7))
                }
            case .error(let msg):
                VStack(spacing: 14) {
                    Image(systemName: "exclamationmark.triangle").font(.system(size: 36)).foregroundStyle(Color.wrenEmber)
                    Text("Can't play \(model.channel.name)").font(.title3.bold())
                    Text(msg).font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    HStack(spacing: 12) {
                        Button("Try Again") { model.retry() }.buttonStyle(.glassCapsule)
                        if model.channels.count > 1 {
                            Button("Next Channel") { model.step(1) }.buttonStyle(.glassCapsule)
                        }
                    }
                }
                .padding(24)
                .frame(maxWidth: 420)
                .background(.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 18))
                .padding()
            case .playing:
                if model.buffering { ProgressView().controlSize(.large).tint(.white) }
            }
            if controlsVisible || model.phase != .playing {
                controls(model).transition(.opacity)
            }
        }
        .foregroundStyle(.white)
    }

    private func controls(_ model: LivePlayerModel) -> some View {
        ZStack {
            Color.black.opacity(0.35).ignoresSafeArea().allowsHitTesting(false)
            VStack(spacing: 0) {
                HStack(alignment: .center, spacing: 12) {
                    Button { dismiss() } label: {
                        Image(systemName: "xmark").font(.system(size: 17, weight: .semibold)).frame(width: 40, height: 40)
                    }
                    .background(.ultraThinMaterial, in: Circle())
                    ChannelLogo(url: state.api?.liveLogoURL(model.channel.logo), name: model.channel.name)
                        .frame(width: 54, height: 34)
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            if let n = model.channel.number { Text(n).font(.subheadline.monospacedDigit()).foregroundStyle(.white.opacity(0.7)) }
                            Text(model.channel.name).font(.headline).lineLimit(1)
                        }
                        if let p = airing(model.channel) {
                            Text(p.title).font(.subheadline).foregroundStyle(.white.opacity(0.8)).lineLimit(1)
                        }
                    }
                    Spacer(minLength: 0)
                    if let pip, AVPictureInPictureController.isPictureInPictureSupported() {
                        Button { pip.startPictureInPicture() } label: { icon("pip.enter") }
                    }
                    AirPlayButton().frame(width: 40, height: 40)
                }
                .padding(.horizontal, 16)
                .padding(.top, 8)
                Spacer()
                HStack(spacing: 12) {
                    Text("LIVE").font(.caption2.bold()).padding(.horizontal, 6).padding(.vertical, 3)
                        .background(Color.wrenEmber, in: RoundedRectangle(cornerRadius: 4))
                    if let p = airing(model.channel) {
                        ProgressView(value: p.progress(at: now)).tint(.white)
                        Text(p.timeRange).font(.caption.monospacedDigit()).foregroundStyle(.white.opacity(0.75)).lineLimit(1)
                    } else {
                        Spacer()
                    }
                    Button { showList = true } label: { icon("list.bullet") }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 12)
            }
            HStack(spacing: 56) {
                Button { model.step(-1); poke() } label: { Image(systemName: "chevron.up").font(.system(size: 28, weight: .semibold)) }
                    .disabled(model.channels.count < 2)
                Button { model.togglePause(); poke() } label: {
                    Image(systemName: model.paused ? "play.fill" : "pause.fill").font(.system(size: 46)).frame(width: 60)
                }
                Button { model.step(1); poke() } label: { Image(systemName: "chevron.down").font(.system(size: 28, weight: .semibold)) }
                    .disabled(model.channels.count < 2)
            }
            .opacity(model.phase == .playing ? 1 : 0)
        }
    }

    private func icon(_ symbol: String) -> some View {
        Image(systemName: symbol).font(.system(size: 18, weight: .medium)).frame(width: 40, height: 40).contentShape(Rectangle())
    }

    private func airing(_ ch: LiveChannel) -> LiveProgram? {
        [ch.now, ch.next].compactMap { $0 }.first { $0.startDate <= now && now < $0.endDate }
    }

    private func poke() {
        withAnimation(.easeOut(duration: 0.2)) { controlsVisible = true }
        hideTask?.cancel()
        hideTask = Task {
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled, model?.phase == .playing, model?.paused != true else { return }
            withAnimation(.easeOut(duration: 0.3)) { controlsVisible = false }
        }
    }

    private func toggleControls() {
        if controlsVisible {
            hideTask?.cancel()
            withAnimation(.easeOut(duration: 0.2)) { controlsVisible = false }
        } else {
            poke()
        }
    }
}

/// The channel list over the picture, for flipping without leaving it.
private struct ChannelPicker: View {
    @Environment(AppState.self) private var state
    let model: LivePlayerModel
    let now: Date
    let close: () -> Void

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
                List(model.channels) { ch in
                    Button {
                        model.tune(ch)
                        close()
                    } label: {
                        HStack(spacing: 12) {
                            ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name).frame(width: 52, height: 32)
                            VStack(alignment: .leading, spacing: 2) {
                                Text([ch.number, ch.name].compactMap { $0 }.joined(separator: "  "))
                                    .font(.subheadline.weight(.semibold)).lineLimit(1)
                                if let p = ch.now, p.endDate > now {
                                    Text(p.title).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                                }
                            }
                            Spacer(minLength: 0)
                            if ch.id == model.channel.id {
                                Image(systemName: "speaker.wave.2.fill").foregroundStyle(Color.wrenEmber)
                            }
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .id(ch.id)
                }
                .listStyle(.plain)
                .onAppear { proxy.scrollTo(model.channel.id, anchor: .center) }
            }
            .navigationTitle("Channels")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) { Button("Done", action: close) }
            }
        }
        .preferredColorScheme(.dark)
    }
}
