package com.wrentv.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CloudDownload
import androidx.compose.material.icons.rounded.Group
import androidx.compose.material.icons.rounded.VideoLibrary
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.data.AddonSetting
import com.wrentv.tv.data.Library
import com.wrentv.tv.data.ServerDownload
import com.wrentv.tv.data.ServerSettings
import com.wrentv.tv.data.ServerUser
import com.wrentv.tv.data.StreamingSettings
import com.wrentv.tv.data.UserDefaultsSettings
import com.wrentv.tv.data.UserLimitsInfo
import com.wrentv.tv.data.UserLimitsPatch
import com.wrentv.tv.data.json
import com.wrentv.tv.ui.Bar
import com.wrentv.tv.ui.Chip
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.ConfirmPanel
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.FormSection
import com.wrentv.tv.ui.ListRow
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNotices
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.Panel
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.ToggleRow
import com.wrentv.tv.ui.TvTextField
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.formatBytes
import com.wrentv.tv.ui.rememberFocus
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.encodeToJsonElement
import kotlinx.serialization.json.put
import java.text.DateFormat
import java.util.Date

/** A server-management page: a title and a column of settings. */
@Composable
private fun AdminPage(title: String, subtitle: String? = null, actions: @Composable () -> Unit = {}, content: LazyListScope.() -> Unit) {
    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = PageGutter, end = PageGutter, top = 40.dp, bottom = 48.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        item("title") {
            Row(Modifier.fillMaxWidth().padding(bottom = 20.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(title, style = MaterialTheme.typography.displaySmall, color = Wren.text)
                    if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
                }
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) { actions() }
            }
        }
        content()
    }
}

/** A blank number field means the default (-1); otherwise at least 0. */
private fun limitNumber(s: String): Int = s.trim().let { if (it.isEmpty()) -1 else (it.toIntOrNull() ?: 0).coerceAtLeast(0) }

// Server downloads

@Composable
fun ServerDownloadsScreen() {
    val state = LocalAppState.current
    var downloads by remember { mutableStateOf<List<ServerDownload>?>(null) }
    var open by remember { mutableStateOf<ServerDownload?>(null) }
    var tick by remember { mutableIntStateOf(0) }
    // Refreshes every few seconds while something is in flight.
    LaunchedEffect(tick) {
        while (true) {
            runCatching { state.api?.serverDownloads() }.getOrNull()?.let { downloads = it }
            if (downloads == null) downloads = emptyList()
            delay(if (downloads.orEmpty().any { it.active }) 3000 else 15000)
        }
    }
    AdminPage("Server Downloads", "Streamed titles being saved to the server as files") {
        val list = downloads
        when {
            list == null -> item("loading") { Loading() }
            list.isEmpty() -> item("empty") {
                EmptyState("Nothing downloading", "Open a streamed title and choose Streaming → Download to Server, or turn on download mode in Server Settings.",
                    icon = Icons.Rounded.CloudDownload)
            }
            else -> items(list, key = { it.id }) { d ->
                Column {
                    ListRow(
                        d.title, { open = d }, Modifier.rememberFocus("sd:${d.id}", default = d == list.first()),
                        detail = d.error?.takeIf { it.isNotEmpty() } ?: when {
                            d.status == "downloading" -> "${formatBytes(d.bytes)} of ${formatBytes(d.total)}"
                            d.status == "done" -> d.path
                            else -> null
                        },
                        value = listOf(d.quality, d.status.replaceFirstChar { it.uppercase() }).filter { it.isNotEmpty() }.joinToString(" · "),
                    )
                    if (d.status == "downloading") Bar(d.fraction, Modifier.padding(start = 18.dp, end = 18.dp, bottom = 8.dp).fillMaxWidth())
                }
            }
        }
    }
    open?.let { d ->
        fun act(f: suspend () -> Unit) {
            open = null
            state.scope.launch { runCatching { f() }; tick++ }
        }
        Panel({ open = null }, d.title) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                if (d.status == "failed" || d.status == "canceled") {
                    ListRow("Retry", { act { state.api?.retryServerDownload(d.id) } }, Modifier.rememberFocus("sd:retry", default = true))
                }
                ListRow(if (d.active) "Cancel Download" else "Clear from List", { act { state.api?.cancelServerDownload(d.id) } },
                    Modifier.rememberFocus("sd:cancel", default = d.status != "failed" && d.status != "canceled"))
            }
        }
    }
}

// Users

@Composable
fun UsersScreen() {
    val state = LocalAppState.current
    var users by remember { mutableStateOf<List<ServerUser>?>(null) }
    var defaults by remember { mutableStateOf(UserDefaultsSettings()) }
    var editing by remember { mutableStateOf<ServerUser?>(null) }
    var tick by remember { mutableIntStateOf(0) }
    LaunchedEffect(tick) {
        val api = state.api ?: return@LaunchedEffect
        users = runCatching { api.users() }.getOrDefault(emptyList())
        runCatching { api.settings() }.getOrNull()?.let { defaults = it.userDefaults }
    }

    fun summary(e: UserLimitsInfo): String = buildList {
        if (!e.canStream && state.server?.streaming == true) add("No streaming")
        add(if (e.maxStreams > 0) "${e.maxStreams} stream${if (e.maxStreams == 1) "" else "s"} at once" else "Unlimited streams")
        add(if (e.canDownload) (if (e.maxDownloads > 0) "${e.maxDownloads} download${if (e.maxDownloads == 1) "" else "s"} at once" else "Downloads") else "No downloads")
    }.joinToString(" · ")

    AdminPage("Users", "Add people and choose their libraries in the web app") {
        val list = users
        when {
            list == null -> item("loading") { Loading() }
            list.isEmpty() -> item("empty") { EmptyState("No users", icon = Icons.Rounded.Group) }
            else -> items(list, key = { it.id }) { u ->
                ListRow(
                    u.displayName.ifEmpty { u.username }, { editing = u }, Modifier.rememberFocus("user:${u.id}", default = u == list.first()),
                    detail = listOfNotNull(u.email?.takeIf { it.isNotEmpty() }, summary(u.effective)).joinToString(" · "),
                    value = if (u.isAdmin) "Admin" else "Member",
                )
            }
        }
    }
    editing?.let { u -> UserLimitsPanel(u, defaults, { editing = null }) { tick++ } }
}

@Composable
private fun UserLimitsPanel(user: ServerUser, defaults: UserDefaultsSettings, close: () -> Unit, saved: () -> Unit) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var allowStreaming by remember { mutableStateOf(user.allowStreaming) }
    var allowDownloads by remember { mutableIntStateOf(user.allowDownloads) }
    var maxStreams by remember { mutableStateOf(if (user.maxStreams >= 0) user.maxStreams.toString() else "") }
    var maxDownloads by remember { mutableStateOf(if (user.maxDownloads >= 0) user.maxDownloads.toString() else "") }
    var picking by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    fun defaultText(n: Int) = "Default (${if (n > 0) n.toString() else "no limit"})"
    val downloadChoices = listOf(
        Choice(-1, "Server default (${if (defaults.deviceDownloads) "allowed" else "off"})"),
        Choice(1, "Allowed"),
        Choice(0, "Not allowed"),
    )

    Panel(close, user.displayName.ifEmpty { user.username }, width = 620.dp) {
        Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            ToggleRow("Can play streamed titles", allowStreaming, { allowStreaming = it }, Modifier.rememberFocus("ul:stream", default = true))
            ListRow("Downloads to devices", { picking = true }, value = downloadChoices.first { it.value == allowDownloads }.label)
            TvTextField(maxStreams, { maxStreams = it.filter(Char::isDigit) }, "Streams at once", placeholder = defaultText(defaults.maxStreams),
                keyboard = KeyboardType.Number, width = 560.dp)
            TvTextField(maxDownloads, { maxDownloads = it.filter(Char::isDigit) }, "Downloads at once", placeholder = defaultText(defaults.maxDownloads),
                keyboard = KeyboardType.Number, width = 560.dp)
            Text(error ?: "Leave a number blank for the server default; 0 means no limit.", style = MaterialTheme.typography.bodySmall,
                color = if (error != null) Wren.bad else Wren.tertiary)
            Spacer(Modifier.height(4.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                PillButton("Save", {
                    scope.launch {
                        try {
                            state.api?.setUserLimits(user.id, UserLimitsPatch(allowStreaming, allowDownloads, limitNumber(maxStreams), limitNumber(maxDownloads)))
                            saved()
                            close()
                        } catch (e: Exception) {
                            error = e.message
                        }
                    }
                }, primary = true)
                PillButton("Cancel", close)
            }
        }
    }
    if (picking) ChoicePanel("Downloads to Devices", downloadChoices, allowDownloads, { picking = false }) { allowDownloads = it }
}

// Libraries

@Composable
fun ManageLibrariesScreen() {
    val state = LocalAppState.current
    val notices = LocalNotices.current
    var open by remember { mutableStateOf<Library?>(null) }
    var removing by remember { mutableStateOf<Library?>(null) }
    var adding by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { state.reloadLibraries() }
    val libraries = state.libraries

    AdminPage("Libraries", actions = {
        PillButton("Add Library", { adding = true }, Modifier.rememberFocus("libs:add"))
    }) {
        if (libraries.isEmpty()) item("empty") { EmptyState("No libraries", "Add a folder of movies or shows.", icon = Icons.Rounded.VideoLibrary) }
        items(libraries, key = { it.id }) { lib ->
            val scanned = lib.lastScanAt?.takeIf { it > 0 }?.let { " · scanned " + DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT).format(Date(it * 1000)) }.orEmpty()
            ListRow(
                lib.name, { open = lib }, Modifier.rememberFocus("lib:${lib.id}", default = lib == libraries.first()),
                detail = if (lib.isStream) "Streamed through the server's debrid service" else "${lib.itemCount ?: 0} items$scanned · ${lib.paths.joinToString(", ")}",
                value = if (lib.type == "shows") "TV Shows" else "Movies",
            )
        }
    }

    open?.let { lib ->
        Panel({ open = null }, lib.name) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                if (!lib.isStream) {
                    ListRow("Scan for New Files", {
                        open = null
                        state.scope.launch {
                            runCatching { state.api?.scanLibrary(lib.id) }
                                .onSuccess { notices.show("Scanning ${lib.name}…") }
                                .onFailure { notices.show(it.message) }
                        }
                    }, Modifier.rememberFocus("lib:scan", default = true))
                }
                ListRow("Remove Library", { open = null; removing = lib }, Modifier.rememberFocus("lib:remove", default = lib.isStream),
                    detail = "Files on disk aren't touched")
            }
        }
    }
    removing?.let { lib ->
        ConfirmPanel("Remove ${lib.name}?", "Its titles and watch history leave Wren TV. The files on disk aren't touched.", "Remove", { removing = null }, destructive = true) {
            state.scope.launch {
                runCatching { state.api?.deleteLibrary(lib.id) }.onFailure { notices.show(it.message) }
                state.reloadLibraries()
                state.refreshTick++
            }
        }
    }
    if (adding) AddLibraryPanel { adding = false }
}

@Composable
private fun AddLibraryPanel(close: () -> Unit) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var name by remember { mutableStateOf("") }
    var type by remember { mutableStateOf("movies") }
    var paths by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    Panel(close, "Add Library", width = 640.dp) {
        Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
            TvTextField(name, { name = it }, "Name", Modifier.rememberFocus("add:name", default = true), placeholder = "Movies", width = 580.dp)
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Chip("Movies", type == "movies", { type = "movies" })
                Chip("TV Shows", type == "shows", { type = "shows" })
            }
            TvTextField(paths, { paths = it }, "Folders", placeholder = "/media/movies", width = 580.dp)
            Text(error ?: "Paths as the server sees them. Separate several with commas.", style = MaterialTheme.typography.bodySmall,
                color = if (error != null) Wren.bad else Wren.tertiary)
            Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                PillButton(if (busy) "Adding…" else "Add", {
                    val list = paths.split(',', '\n').map { it.trim() }.filter { it.isNotEmpty() }
                    busy = true
                    scope.launch {
                        try {
                            state.api?.createLibrary(name.trim(), type, list)
                            state.reloadLibraries()
                            state.refreshTick++
                            close()
                        } catch (e: Exception) {
                            error = e.message
                        } finally {
                            busy = false
                        }
                    }
                }, primary = true, enabled = !busy && name.isNotBlank() && paths.isNotBlank())
                PillButton("Cancel", close)
            }
        }
    }
}

// Server settings

private enum class SettingsTab(val label: String) { General("General"), Streaming("Streaming"), Users("Users") }

private val Qualities = listOf("2160p" to "4K", "1080p" to "1080p", "720p" to "720p")
private val HwAccels = listOf("auto", "videotoolbox", "nvenc", "qsv", "vaapi", "amf", "none")

/** A field that edits a number; blank is 0. */
@Composable
private fun NumberField(label: String, value: Double, onChange: (Double) -> Unit, unit: String = "", whole: Boolean = false) {
    var text by remember { mutableStateOf(if (value == 0.0) "" else if (whole || value % 1.0 == 0.0) value.toLong().toString() else value.toString()) }
    TvTextField(
        text, { t ->
            text = t.filter { it.isDigit() || (!whole && it == '.') }
            onChange(text.toDoubleOrNull() ?: 0.0)
        },
        if (unit.isEmpty()) label else "$label ($unit)", placeholder = "No limit",
        keyboard = if (whole) KeyboardType.Number else KeyboardType.Decimal, width = 420.dp,
    )
}

@Composable
fun ServerSettingsScreen() {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var tab by remember { mutableStateOf(SettingsTab.General) }
    var settings by remember { mutableStateOf<ServerSettings?>(null) }
    var serverName by remember { mutableStateOf("") }
    var publicUrl by remember { mutableStateOf("") }
    var hwaccel by remember { mutableStateOf("auto") }
    var tmdbKey by remember { mutableStateOf("") }
    var omdbKey by remember { mutableStateOf("") }
    var fingerprinting by remember { mutableStateOf(true) }
    var streaming by remember { mutableStateOf(StreamingSettings()) }
    var userDefaults by remember { mutableStateOf(UserDefaultsSettings()) }
    val keyEdits = remember { mutableStateMapOf<String, String>() }
    val disconnects = remember { mutableStateMapOf<String, Boolean>() }
    val accounts = remember { mutableStateMapOf<String, String>() }
    var newAddon by remember { mutableStateOf("") }
    var addonError by remember { mutableStateOf<String?>(null) }
    var status by remember { mutableStateOf<String?>(null) }
    var saving by remember { mutableStateOf(false) }
    var picker by remember { mutableStateOf<String?>(null) }
    // Bumped on load so the number fields pick up the saved values.
    var generation by remember { mutableIntStateOf(0) }

    fun apply(s: ServerSettings) {
        settings = s
        serverName = s.serverName
        publicUrl = s.publicUrl
        hwaccel = s.hwaccel.ifEmpty { "auto" }
        tmdbKey = ""
        omdbKey = ""
        fingerprinting = s.markerFingerprinting ?: true
        streaming = s.streaming
        userDefaults = s.userDefaults
        keyEdits.clear()
        disconnects.clear()
        accounts.clear()
        generation++
    }

    LaunchedEffect(Unit) {
        try {
            state.api?.settings()?.let(::apply)
        } catch (e: Exception) {
            status = e.message
        }
    }

    fun save() {
        val st = streaming
        val stream = buildJsonObject {
            put("enabled", st.enabled)
            put("prefetch", st.prefetch)
            put("lookupOnOpen", st.lookupOnOpen)
            put("providerOrder", JsonArray(st.debrid.map { JsonPrimitive(it.provider) }))
            put("addons", json.encodeToJsonElement(st.addons))
            put("qualities", json.encodeToJsonElement(st.qualities))
            put("maxSizeGB", json.encodeToJsonElement(st.maxSizeGB ?: emptyMap()))
            put("mode", st.mode)
            put("movieLibrary", st.movieLibrary)
            put("showLibrary", st.showLibrary)
            put("downloadQuality", st.downloadQuality)
            put("limits", json.encodeToJsonElement(st.limits))
            keyEdits.forEach { (provider, key) -> key.trim().takeIf { it.isNotEmpty() }?.let { put(provider + "Key", it) } }
            disconnects.keys.forEach { put(it + "Key", "") }
        }
        val patch = buildJsonObject {
            put("serverName", serverName)
            put("publicUrl", publicUrl)
            put("hwaccel", hwaccel)
            put("webhooks", json.encodeToJsonElement(settings?.webhooks.orEmpty().filter { it.url.isNotEmpty() }))
            put("markerFingerprinting", fingerprinting)
            put("streaming", stream)
            put("userDefaults", json.encodeToJsonElement(userDefaults))
            if (tmdbKey.isNotBlank()) put("tmdbApiKey", tmdbKey.trim())
            if (omdbKey.isNotBlank()) put("omdbApiKey", omdbKey.trim())
        }
        saving = true
        status = "Saving…"
        scope.launch {
            try {
                state.api?.patchSettings(patch)?.let { s ->
                    apply(s)
                    status = if (s.restartRequired == true) "Saved — restart the server to apply" else "Saved"
                    state.reloadLibraries()
                }
            } catch (e: Exception) {
                status = e.message
            } finally {
                saving = false
            }
        }
    }

    if (settings == null) {
        AdminPage("Server Settings") { item("loading") { if (status != null) EmptyState("Couldn't load", status) else Loading() } }
        return
    }

    AdminPage("Server Settings", status, actions = {
        PillButton(if (saving) "Saving…" else "Save", ::save, Modifier.rememberFocus("ss:save"), primary = true, enabled = !saving)
    }) {
        item("tabs") {
            Row(Modifier.padding(bottom = 16.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                SettingsTab.entries.forEach { t -> Chip(t.label, tab == t, { tab = t }, Modifier.rememberFocus("ss:tab:${t.name}", default = t == SettingsTab.General)) }
            }
        }
        when (tab) {
            SettingsTab.General -> item("general") {
                Column {
                    FormSection("Server") {
                        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            TvTextField(serverName, { serverName = it }, "Name", width = 640.dp)
                            TvTextField(publicUrl, { publicUrl = it }, "Public URL", placeholder = "https://wrentv.example.com", keyboard = KeyboardType.Uri, width = 640.dp)
                        }
                    }
                    FormSection("Metadata", "OMDb adds Rotten Tomatoes, IMDb and Metacritic scores; get a free key at omdbapi.com.") {
                        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            TvTextField(tmdbKey, { tmdbKey = it }, "TMDB API key", password = true, width = 640.dp,
                                placeholder = if (settings?.tmdbKeySet == true) "Saved — enter to replace" else "Required for artwork, metadata and streaming")
                            TvTextField(omdbKey, { omdbKey = it }, "OMDb API key", password = true, width = 640.dp,
                                placeholder = if (settings?.omdbKeySet == true) "Saved — enter to replace" else "Optional")
                        }
                    }
                    FormSection("Playback", "Audio detection compares episodes of a season to find shared intros and credits. Chapters are always used.") {
                        ListRow("Hardware transcoding", { picker = "hwaccel" }, value = hwaccel)
                        ToggleRow("Detect intros and credits from audio", fingerprinting, { fingerprinting = it })
                    }
                }
            }
            SettingsTab.Streaming -> item("streaming-$generation") {
                val st = streaming
                Column {
                    val statusText = when {
                        !st.enabled -> "Off"
                        st.active -> "On"
                        settings?.tmdbKeySet != true -> "Needs a TMDB API key"
                        st.debrid.none { it.keySet } -> "Needs a debrid API key"
                        st.addons.none { it.enabled } -> "Needs an addon"
                        else -> "Starting…"
                    }
                    FormSection("Streaming", "Pressing play finds a cached release through your debrid service and streams it through this server. Only the link is saved, never the video, unless downloads are on.") {
                        ToggleRow("Enable streaming", st.enabled, { streaming = st.copy(enabled = it) }, detail = statusText)
                        ToggleRow("Find streams for the rest of the season in the background", st.prefetch, { streaming = st.copy(prefetch = it) })
                        ToggleRow("Find streams when a details page opens", st.lookupOnOpen, { streaming = st.copy(lookupOnOpen = it) })
                    }
                    FormSection("Debrid services", "Your keys are used for everyone, tried top to bottom.") {
                        st.debrid.forEachIndexed { i, d ->
                            Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text(d.name.ifEmpty { d.provider }, style = MaterialTheme.typography.titleMedium, color = Wren.text)
                                Text(accounts[d.provider] ?: if (disconnects.containsKey(d.provider)) "Disconnects when you save"
                                    else if (d.keySet) "Key ending ${d.keyHint ?: "••••"}" else "Not connected",
                                    style = MaterialTheme.typography.bodySmall, color = Wren.secondary)
                                TvTextField(keyEdits[d.provider] ?: "", { keyEdits[d.provider] = it; disconnects.remove(d.provider) }, "",
                                    password = true, placeholder = if (d.keySet) "Paste a new key to replace" else "API key", width = 640.dp)
                                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                    Chip("Check", false, {
                                        accounts[d.provider] = "Checking…"
                                        scope.launch {
                                            accounts[d.provider] = try {
                                                val a = state.api?.checkDebrid(d.provider, keyEdits[d.provider] ?: "")
                                                when {
                                                    a == null -> ""
                                                    a.ok -> listOfNotNull(if (a.premium) "Premium" else "Not premium", a.user, a.expires?.take(10)?.let { "until $it" }).joinToString(" · ")
                                                    else -> a.error ?: "Key rejected"
                                                }
                                            } catch (e: Exception) {
                                                e.message ?: "Check failed"
                                            }
                                        }
                                    })
                                    if (i > 0) Chip("Move Up", false, {
                                        val l = st.debrid.toMutableList(); l.add(i - 1, l.removeAt(i)); streaming = st.copy(debrid = l)
                                    })
                                    if (d.keySet && !disconnects.containsKey(d.provider)) Chip("Disconnect", false, {
                                        keyEdits.remove(d.provider); disconnects[d.provider] = true
                                    })
                                }
                            }
                        }
                    }
                    FormSection("Addons", addonError ?: "Stremio addons that list torrents for a title. Results from every enabled addon are merged.") {
                        st.addons.forEachIndexed { i, a ->
                            ToggleRow(a.name, a.enabled, { on ->
                                streaming = st.copy(addons = st.addons.mapIndexed { j, x -> if (j == i) x.copy(enabled = on) else x })
                            }, detail = a.url)
                        }
                        Row(Modifier.padding(12.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.Bottom) {
                            TvTextField(newAddon, { newAddon = it }, "Add addon", placeholder = "https://…/manifest.json", keyboard = KeyboardType.Uri, width = 520.dp)
                            PillButton("Add", {
                                val url = newAddon.trim()
                                addonError = null
                                scope.launch {
                                    try {
                                        val a = state.api?.checkAddon(url) ?: return@launch
                                        if (streaming.addons.any { it.url == a.url }) throw Exception("That addon is already in the list")
                                        val named: AddonSetting = a.copy(name = a.name.ifEmpty { runCatching { java.net.URI(a.url).host }.getOrNull() ?: "addon" }.lowercase())
                                        streaming = streaming.copy(addons = streaming.addons + named)
                                        newAddon = ""
                                    } catch (e: Exception) {
                                        addonError = e.message
                                    }
                                }
                            }, enabled = newAddon.isNotBlank())
                        }
                    }
                    FormSection("Quality", "A stream is saved for each quality. Size limits apply to the file that plays; blank is any size.") {
                        Qualities.forEach { (q, label) ->
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                ToggleRow(label, q in st.qualities, { on ->
                                    val qs = st.qualities.filter { it != q } + if (on) listOf(q) else emptyList()
                                    streaming = st.copy(qualities = Qualities.map { it.first }.filter { it in qs })
                                }, Modifier.weight(1f))
                                Column(Modifier.padding(horizontal = 12.dp)) {
                                    NumberField("Max size", st.maxSizeGB?.get(q) ?: 0.0, { v ->
                                        streaming = streaming.copy(maxSizeGB = (streaming.maxSizeGB ?: emptyMap()) + (q to v.coerceAtLeast(0.0)))
                                    }, unit = "GB")
                                }
                            }
                        }
                    }
                    FormSection("Saving to the server", "Stream only writes nothing to your media folders; admins can still download single titles. Download mode saves each streamed title as a regular file.") {
                        ListRow("Mode", { picker = "mode" }, value = if (st.mode == "download") "Download what's watched" else "Stream only")
                        ListRow("Movies go to", { picker = "movieLibrary" }, value = state.libraries.firstOrNull { it.id == st.movieLibrary }?.name ?: "None")
                        ListRow("Shows go to", { picker = "showLibrary" }, value = state.libraries.firstOrNull { it.id == st.showLibrary }?.name ?: "None")
                        ListRow("Download quality", { picker = "downloadQuality" }, value = Qualities.firstOrNull { it.first == st.downloadQuality }?.second ?: st.downloadQuality)
                    }
                    FormSection("Limits", "Blank means no limit.") {
                        val l = st.limits
                        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                                NumberField("All debrid traffic", l.bandwidthMbps, { streaming = streaming.copy(limits = streaming.limits.copy(bandwidthMbps = it)) }, "Mbps")
                                NumberField("Each stream", l.perStreamMbps, { streaming = streaming.copy(limits = streaming.limits.copy(perStreamMbps = it)) }, "Mbps")
                            }
                            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                                NumberField("Server downloads", l.downloadMbps, { streaming = streaming.copy(limits = streaming.limits.copy(downloadMbps = it)) }, "Mbps")
                                NumberField("Streams at once (all users)", l.maxStreams.toDouble(), { streaming = streaming.copy(limits = streaming.limits.copy(maxStreams = it.toInt())) }, whole = true)
                            }
                            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                                NumberField("Server downloads at once", l.maxDownloads.toDouble(), { streaming = streaming.copy(limits = streaming.limits.copy(maxDownloads = it.toInt())) }, whole = true)
                                NumberField("Background lookups at once", l.prefetchWorkers.toDouble(), { streaming = streaming.copy(limits = streaming.limits.copy(prefetchWorkers = it.toInt())) }, whole = true)
                            }
                            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                                NumberField("Total downloaded", l.downloadQuotaGB, { streaming = streaming.copy(limits = streaming.limits.copy(downloadQuotaGB = it)) }, "GB")
                                NumberField("Keep this much disk free", l.minFreeGB, { streaming = streaming.copy(limits = streaming.limits.copy(minFreeGB = it)) }, "GB")
                            }
                        }
                    }
                }
            }
            SettingsTab.Users -> item("users-$generation") {
                FormSection("Defaults for everyone", "Applies unless a person's own settings say otherwise (Settings → Users). Blank means no limit.") {
                    ToggleRow("Let people download movies and episodes to their devices", userDefaults.deviceDownloads, { userDefaults = userDefaults.copy(deviceDownloads = it) })
                    Row(Modifier.padding(12.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        NumberField("Streams at once per person", userDefaults.maxStreams.toDouble(), { userDefaults = userDefaults.copy(maxStreams = it.toInt()) }, whole = true)
                        NumberField("Device downloads at once per person", userDefaults.maxDownloads.toDouble(), { userDefaults = userDefaults.copy(maxDownloads = it.toInt()) }, whole = true)
                    }
                }
            }
        }
    }

    val st = streaming
    when (picker) {
        "hwaccel" -> ChoicePanel("Hardware Transcoding", HwAccels.map { Choice(it, it) }, hwaccel, { picker = null }) { hwaccel = it }
        "mode" -> ChoicePanel("Mode", listOf(Choice("stream", "Stream only"), Choice("download", "Download what's watched")), st.mode, { picker = null }) {
            streaming = st.copy(mode = it)
        }
        "movieLibrary", "showLibrary" -> {
            val movies = picker == "movieLibrary"
            val libs = state.libraries.filter { !it.isStream && it.type == if (movies) "movies" else "shows" }
            ChoicePanel(if (movies) "Movies Go To" else "Shows Go To", listOf(Choice("", "None")) + libs.map { Choice(it.id, it.name) },
                if (movies) st.movieLibrary else st.showLibrary, { picker = null }) {
                streaming = if (movies) st.copy(movieLibrary = it) else st.copy(showLibrary = it)
            }
        }
        "downloadQuality" -> ChoicePanel("Download Quality", Qualities.map { Choice(it.first, it.second) }, st.downloadQuality, { picker = null }) {
            streaming = st.copy(downloadQuality = it)
        }
    }
}
