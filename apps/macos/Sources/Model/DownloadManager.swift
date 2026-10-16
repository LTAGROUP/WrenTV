import AppKit
import Foundation
import Observation

/// A movie or episode saved on this Mac for offline viewing.
struct OfflineItem: Codable, Identifiable, Hashable {
    enum Status: String, Codable {
        case waiting, preparing, downloading, paused, done, failed
    }

    let id: String // the server's item id
    var server: String
    var kind: String
    var title: String
    var showId: String?
    var showTitle: String?
    var seasonNumber: Int
    var episodeNumber: Int
    var year: Int?
    var overview: String?
    var runtimeMin: Int?
    var posterPath: String?
    var fileId: String?
    var label: String?
    var fileName: String?
    var size: Int64 = 0
    var received: Int64 = 0
    var status: Status = .waiting
    var message: String?
    /// The video's name in the downloads folder once complete.
    var file: String?
    /// The poster's name in the downloads folder.
    var poster: String?
    var position: Double = 0
    var duration: Double = 0
    var addedAt = Date()

    var isEpisode: Bool { kind == "episode" }
    var displayTitle: String { isEpisode ? (showTitle ?? title) : title }
    var subtitle: String? { isEpisode ? "S\(seasonNumber) · E\(episodeNumber) — \(title)" : nil }
    var fraction: Double { size > 0 ? min(1, Double(received) / Double(size)) : 0 }
    var active: Bool { status == .preparing || status == .downloading }
}

/// Saves the original file of a movie or episode on this Mac through a
/// short-lived link from the server, resuming interrupted transfers, and keeps
/// an index so downloads play without a connection.
@MainActor
@Observable
final class DownloadManager {
    private(set) var items: [OfflineItem] = []

    @ObservationIgnored private var api: WrenTVAPI?
    @ObservationIgnored private var maxConcurrent = 2
    @ObservationIgnored private var tasks: [String: URLSessionDownloadTask] = [:]
    @ObservationIgnored private var preparing: [String: Task<Void, Never>] = [:]
    @ObservationIgnored private var lastSave = Date.distantPast
    @ObservationIgnored private lazy var delegate = DownloadDelegate(manager: self)
    @ObservationIgnored private lazy var session: URLSession = {
        let c = URLSessionConfiguration.default
        c.timeoutIntervalForRequest = 60
        c.timeoutIntervalForResource = 7 * 24 * 3600
        return URLSession(configuration: c, delegate: delegate, delegateQueue: .main)
    }()

    let folder: URL = {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let dir = base.appendingPathComponent("Wren TV/Downloads", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }()

    private var indexURL: URL { folder.appendingPathComponent("downloads.json") }

    init() {
        if let data = try? Data(contentsOf: indexURL),
           let saved = try? JSONDecoder().decode([OfflineItem].self, from: data) {
            // Transfers don't survive a quit; they resume once signed in.
            items = saved.map { var x = $0; if x.active { x.status = .waiting; x.message = nil }; return x }
        }
    }

    // MARK: Connection

    func connect(api: WrenTVAPI, limits: UserLimitsInfo?) {
        self.api = api
        let m = limits?.maxDownloads ?? 2
        maxConcurrent = m > 0 ? min(m, 4) : 3
        pump()
    }

    func disconnect() {
        api = nil
        for t in preparing.values { t.cancel() }
        preparing = [:]
    }

    // MARK: Queries

    func item(_ id: String) -> OfflineItem? { items.first { $0.id == id } }

    func localURL(_ x: OfflineItem) -> URL? {
        guard x.status == .done, let f = x.file else { return nil }
        let url = folder.appendingPathComponent(f)
        return FileManager.default.fileExists(atPath: url.path) ? url : nil
    }

    func posterURL(_ x: OfflineItem) -> URL? { x.poster.map { folder.appendingPathComponent($0) } }

    var usedBytes: Int64 { items.filter { $0.status == .done }.reduce(0) { $0 + $1.size } }

    // MARK: Changes

    /// Queues a movie or episode. `show` names the show for episodes.
    func add(_ it: Item, show: Item?, fileId: String? = nil, server: String) {
        guard it.isPlayable else { return }
        if let i = items.firstIndex(where: { $0.id == it.id }) {
            if items[i].status == .failed || items[i].status == .paused { resume(it.id) }
            return
        }
        let x = OfflineItem(id: it.id, server: server, kind: it.kind, title: it.title, showId: it.showId ?? show?.id,
                            showTitle: show?.title ?? it.showTitle, seasonNumber: it.seasonNumber, episodeNumber: it.episodeNumber,
                            year: it.year ?? show?.year, overview: it.overview, runtimeMin: it.runtimeMin,
                            posterPath: it.kind == "episode" ? (it.stillPath ?? show?.posterPath) : it.posterPath, fileId: fileId)
        items.append(x)
        save()
        pump()
    }

    func pause(_ id: String) {
        preparing[id]?.cancel()
        preparing[id] = nil
        if let t = tasks.removeValue(forKey: id) {
            t.cancel { [weak self] data in
                Task { @MainActor in
                    if let data { try? data.write(to: self?.resumeURL(id) ?? URL(fileURLWithPath: "/dev/null")) }
                }
            }
        }
        update(id) { $0.status = .paused; $0.message = nil }
        pump()
    }

    func resume(_ id: String) {
        update(id) { $0.status = .waiting; $0.message = nil }
        pump()
    }

    func remove(_ id: String) {
        preparing[id]?.cancel()
        preparing[id] = nil
        tasks.removeValue(forKey: id)?.cancel()
        if let x = item(id) {
            for name in [x.file, x.poster].compactMap({ $0 }) {
                try? FileManager.default.removeItem(at: folder.appendingPathComponent(name))
            }
        }
        try? FileManager.default.removeItem(at: resumeURL(id))
        items.removeAll { $0.id == id }
        save()
        pump()
    }

    func savePosition(_ id: String, position: Double, duration: Double) {
        update(id) {
            $0.position = position
            if duration > 0 { $0.duration = duration }
        }
        save()
    }

    func revealInFinder(_ x: OfflineItem) {
        if let url = localURL(x) { NSWorkspace.shared.activateFileViewerSelecting([url]) }
    }

    // MARK: Transfers

    /// Starts waiting downloads up to the per-user limit.
    private func pump() {
        guard api != nil else { return }
        var running = items.filter(\.active).count
        for x in items where x.status == .waiting {
            guard running < maxConcurrent else { break }
            running += 1
            start(x.id)
        }
    }

    private func start(_ id: String) {
        guard let api, let x = item(id) else { return }
        update(id) { $0.status = .preparing; $0.message = nil }
        preparing[id] = Task { [weak self] in
            defer { self?.preparing[id] = nil }
            do {
                var link: DeviceDownload
                var tries = 0
                while true {
                    link = try await api.deviceDownload(id, fileId: x.fileId)
                    if link.status != "pending" { break }
                    tries += 1
                    if tries > 40 { throw WrenTVError(message: "No stream found yet. Try again later.") }
                    self?.update(id) { $0.message = link.message ?? "Finding a stream…" }
                    try await Task.sleep(for: .seconds(3))
                }
                guard let path = link.url, let self, !Task.isCancelled else { return }
                let url = api.absolute(path)
                self.update(id) {
                    $0.fileName = link.fileName
                    $0.fileId = link.fileId ?? $0.fileId
                    $0.label = link.label
                    if let s = link.size, s > 0 { $0.size = s }
                    $0.status = .downloading
                    $0.message = nil
                }
                self.save()
                let resume = try? Data(contentsOf: self.resumeURL(id))
                try? FileManager.default.removeItem(at: self.resumeURL(id))
                // Resume data is tied to the old link; a fresh link restarts the transfer
                // when the server no longer accepts it.
                let task = resume.map { self.session.downloadTask(withResumeData: $0) } ?? self.session.downloadTask(with: url)
                task.taskDescription = id
                self.tasks[id] = task
                task.resume()
                await self.fetchPoster(id, api: api)
            } catch is CancellationError {
            } catch {
                self?.fail(id, error.localizedDescription)
            }
        }
    }

    private func fetchPoster(_ id: String, api: WrenTVAPI) async {
        guard let x = item(id), x.poster == nil, let url = api.imageURL(x.posterPath, size: x.isEpisode ? "w300" : "w342"),
              let (data, _) = try? await URLSession.shared.data(from: url), !data.isEmpty else { return }
        let name = "\(id).jpg"
        try? data.write(to: folder.appendingPathComponent(name))
        update(id) { $0.poster = name }
        save()
    }

    private func fail(_ id: String, _ message: String) {
        tasks[id] = nil
        update(id) { $0.status = .failed; $0.message = message }
        save()
        pump()
    }

    private func resumeURL(_ id: String) -> URL { folder.appendingPathComponent("\(id).resume") }

    // MARK: Delegate callbacks (main queue)

    fileprivate func progress(_ task: URLSessionDownloadTask, written: Int64, expected: Int64) {
        guard let id = task.taskDescription else { return }
        update(id) {
            $0.received = written
            if expected > 0 { $0.size = expected }
        }
        if Date().timeIntervalSince(lastSave) > 5 { save() }
    }

    fileprivate func finished(_ task: URLSessionDownloadTask, at location: URL) {
        guard let id = task.taskDescription, tasks[id] === task else { return }
        tasks[id] = nil
        let status = (task.response as? HTTPURLResponse)?.statusCode ?? 0
        if status >= 400 {
            let body = (try? Data(contentsOf: location)).flatMap { try? JSONDecoder().decode([String: String].self, from: $0) }
            if status == 429 {
                // Over this user's download limit; wait for another to finish.
                update(id) { $0.status = .paused; $0.message = body?["error"] }
                save()
                Task { [weak self] in
                    try? await Task.sleep(for: .seconds(30))
                    if self?.item(id)?.status == .paused { self?.resume(id) }
                }
                return
            }
            fail(id, body?["error"] ?? HTTPURLResponse.localizedString(forStatusCode: status))
            return
        }
        let ext = ((item(id)?.fileName ?? "") as NSString).pathExtension
        let name = "\(id).\(ext.isEmpty ? "mkv" : ext)"
        let dest = folder.appendingPathComponent(name)
        try? FileManager.default.removeItem(at: dest)
        do {
            try FileManager.default.moveItem(at: location, to: dest)
        } catch {
            fail(id, error.localizedDescription)
            return
        }
        let size = (try? FileManager.default.attributesOfItem(atPath: dest.path))?[.size] as? Int64
        update(id) {
            $0.file = name
            $0.status = .done
            $0.message = nil
            if let size { $0.size = size; $0.received = size }
        }
        save()
        pump()
    }

    fileprivate func completed(_ task: URLSessionTask, error: Error?) {
        guard let error, let id = task.taskDescription, tasks[id] === task else { return }
        let ns = error as NSError
        if let data = ns.userInfo[NSURLSessionDownloadTaskResumeData] as? Data {
            try? data.write(to: resumeURL(id))
        }
        if ns.code == NSURLErrorCancelled { return }
        fail(id, error.localizedDescription)
    }

    // MARK: Index

    private func update(_ id: String, _ f: (inout OfflineItem) -> Void) {
        guard let i = items.firstIndex(where: { $0.id == id }) else { return }
        f(&items[i])
    }

    private func save() {
        lastSave = Date()
        if let data = try? JSONEncoder().encode(items) { try? data.write(to: indexURL, options: .atomic) }
    }
}

/// URLSession's delegate must be an NSObject; it hands events to the manager
/// on the main queue (the session's delegate queue).
private final class DownloadDelegate: NSObject, URLSessionDownloadDelegate {
    weak var manager: DownloadManager?

    init(manager: DownloadManager) { self.manager = manager }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didWriteData bytesWritten: Int64,
                    totalBytesWritten: Int64, totalBytesExpectedToWrite: Int64) {
        MainActor.assumeIsolated { manager?.progress(downloadTask, written: totalBytesWritten, expected: totalBytesExpectedToWrite) }
    }

    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didFinishDownloadingTo location: URL) {
        // The file is deleted when this returns, so it is moved synchronously.
        MainActor.assumeIsolated { manager?.finished(downloadTask, at: location) }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        MainActor.assumeIsolated { manager?.completed(task, error: error) }
    }
}
