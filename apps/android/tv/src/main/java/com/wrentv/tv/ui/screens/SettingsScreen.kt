package com.wrentv.tv.ui.screens

import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.mutableIntStateOf
import com.wrentv.tv.data.WrenCollection
import com.wrentv.tv.data.ContinueAfters
import com.wrentv.tv.data.OfflineItem
import com.wrentv.tv.data.Tracker
import com.wrentv.tv.data.TrackerStatus
import com.wrentv.tv.data.UserPrefs
import com.wrentv.tv.data.WatchedPercents
import com.wrentv.tv.data.continueAfterLabel
import com.wrentv.tv.data.languageName
import com.wrentv.tv.ui.QrCode
import kotlinx.coroutines.delay
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.ConfirmPanel
import com.wrentv.tv.ui.FormSection
import com.wrentv.tv.ui.ListRow
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.LocalNotices
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.ReportScroll
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.ToggleRow
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.formatBytes
import com.wrentv.tv.ui.rememberFocus
import kotlinx.coroutines.launch
import java.util.Locale

/** ISO 639-1 codes the server understands. */
private val Languages = listOf(
    "en", "es", "fr", "de", "it", "pt", "ja", "ko", "zh", "hi", "ru", "ar", "nl", "sv", "no", "da", "fi",
    "pl", "tr", "el", "he", "th", "vi", "id", "cs", "hu", "ro", "uk", "ta", "te", "tl", "fa",
)

private fun languageChoices(none: String): List<Choice<String>> =
    listOf(Choice("", none)) + Languages.map { Choice(it, languageName(it) ?: it) }.sortedBy { it.label.lowercase(Locale.getDefault()) }

private enum class SettingsPanel { Audio, Subtitles, Watched, ContinueAfter, PosterSize, Screensaver, SignOut }

private val posterSizes = listOf(Choice("small", "Small"), Choice("medium", "Medium"), Choice("large", "Large"))

@Composable
fun SettingsScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val notices = LocalNotices.current
    val context = LocalContext.current
    val list = rememberLazyListState()
    ReportScroll(list)
    var prefs by remember { mutableStateOf<UserPrefs?>(null) }
    var panel by remember { mutableStateOf<SettingsPanel?>(null) }
    LaunchedEffect(state.user?.id) { prefs = runCatching { state.api?.prefs() }.getOrNull() }
    var collections by remember { mutableStateOf<List<WrenCollection>?>(null) }
    LaunchedEffect(state.user?.id) { collections = runCatching { state.api?.collections() }.getOrNull() }

    fun save(p: UserPrefs) {
        val before = prefs
        prefs = p
        state.scope.launch {
            try {
                state.api?.setPrefs(p)?.let { prefs = it }
            } catch (e: Exception) {
                prefs = before
                notices.show("Couldn't save: ${e.message}")
            }
        }
    }

    val user = state.user
    val server = state.server
    val appVersion = remember {
        runCatching { context.packageManager.getPackageInfo(context.packageName, 0).versionName }.getOrNull() ?: ""
    }

    LazyColumn(
        Modifier.fillMaxSize(), state = list,
        contentPadding = PaddingValues(top = TopBarSpace, bottom = 48.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        item("header") {
            Text("Settings", style = MaterialTheme.typography.displaySmall, color = Wren.text, modifier = Modifier.width(860.dp).padding(top = 28.dp, bottom = 24.dp))
        }
        item("account") {
            Column(Modifier.width(860.dp)) {
                FormSection("Account") {
                    ListRow(
                        user?.displayName?.ifEmpty { null } ?: user?.username ?: "",
                        {}, Modifier.rememberFocus("set:account", default = true),
                        detail = listOfNotNull(state.accountEmail ?: user?.username, roleName(user?.role, user?.isAdmin == true)).joinToString(" · "),
                    )
                    ListRow(server?.name ?: "Server", {}, detail = connectionLabel(state.api?.baseUrl?.toString()), value = server?.version?.let { "v$it" })
                }
                FormSection("Playback", "Movies and episodes start with audio and subtitles in these languages when they have them. Saved to your account, so it applies on every device.") {
                    val p = prefs
                    ListRow("Audio language", { panel = SettingsPanel.Audio }, Modifier.rememberFocus("set:audio"),
                        value = if (p == null) "…" else languageName(p.audioLanguage) ?: "File's default", enabled = p != null)
                    ListRow("Subtitles", { panel = SettingsPanel.Subtitles }, Modifier.rememberFocus("set:subs"),
                        value = if (p == null) "…" else languageName(p.subtitleLanguage) ?: "Off", enabled = p != null)
                    ToggleRow("Forced subtitles", p?.forcedSubtitles ?: true, { on -> p?.let { save(it.copy(forcedSubtitles = on)) } },
                        Modifier.rememberFocus("set:forced"), detail = "With subtitles off, still show foreign-language lines and signs")
                    ToggleRow("Subtitles with foreign audio", p?.foreignAudioSubtitles ?: true, { on -> p?.let { save(it.copy(foreignAudioSubtitles = on)) } },
                        Modifier.rememberFocus("set:foreign"), detail = "When a title plays in another language, show subtitles in your audio language")
                }
                FormSection("Watching", "Saved to your account, so it applies on every device.") {
                    val p = prefs
                    ListRow("Counts as watched at", { panel = SettingsPanel.Watched }, Modifier.rememberFocus("set:watched"),
                        detail = "How far in a movie or episode is marked watched", value = if (p == null) "…" else "${p.watchedPercent}%", enabled = p != null)
                    ListRow("Add to Continue Watching after", { panel = SettingsPanel.ContinueAfter }, Modifier.rememberFocus("set:continue"),
                        detail = "How long you watch before it's on Continue Watching", value = if (p == null) "…" else continueAfterLabel(p.continueAfter), enabled = p != null)
                }
                FormSection("Browsing") {
                    ListRow("Layouts", { nav.push(Route.Layouts) }, Modifier.rememberFocus("set:layouts"),
                        detail = if (state.isAdmin) "Rows on Home and each library, yours and everyone's" else "The rows on Home and each library")
                    ListRow("Poster Size", { panel = SettingsPanel.PosterSize }, Modifier.rememberFocus("set:posters"),
                        detail = "How big titles are on Home and as you browse, saved to your account",
                        value = posterSizes.firstOrNull { it.value == state.posterSize }?.label ?: "Medium")
                }
                Tracker.entries.forEach { TrackerSection(it) }
                FormSection("Sound", "Kept on this TV.") {
                    ListRow("Interface Sounds", { state.uiSounds = !state.uiSounds }, Modifier.rememberFocus("set:sounds"),
                        detail = "A click as you move around", value = if (state.uiSounds) "On" else "Off")
                    ListRow("Theme Music", { state.themeMusic = !state.themeMusic }, Modifier.rememberFocus("set:theme"),
                        detail = "Plays a show's theme on its page", value = if (state.themeMusic) "On" else "Off")
                }
                FormSection("Screensaver", "Comes up after a few idle minutes. Kept on this TV.") {
                    ListRow("Show", { panel = SettingsPanel.Screensaver }, Modifier.rememberFocus("set:screensaver"),
                        detail = "Artwork that drifts past, with the time", value = screensaverLabel(state.screensaver, collections))
                }
                val saved = state.downloads.items
                if (saved.isNotEmpty() || state.limits?.canDownload == true) {
                    FormSection("Downloads on this TV") {
                        val done = saved.count { it.status == OfflineItem.Status.DONE }
                        ListRow("Downloads", { nav.select(Route.Downloads) }, Modifier.rememberFocus("set:downloads"),
                            detail = "$done saved" + (saved.size - done).takeIf { it > 0 }?.let { " · $it in progress" }.orEmpty(),
                            value = formatBytes(saved.sumOf { if (it.status == OfflineItem.Status.DONE) it.size else it.received }))
                    }
                }
                if (state.isAdmin) {
                    FormSection("Server") {
                        ListRow("Activity", { nav.push(Route.Stats) }, Modifier.rememberFocus("set:stats"), detail = "Who's watching, plays and the library")
                        ListRow("Libraries", { nav.push(Route.ManageLibraries) }, Modifier.rememberFocus("set:libraries"), detail = "Scan, add or remove libraries")
                        ListRow("Users", { nav.push(Route.Users) }, Modifier.rememberFocus("set:users"), detail = "Streaming and download limits")
                        if (server?.streaming == true) {
                            ListRow("Server Downloads", { nav.push(Route.ServerDownloads) }, Modifier.rememberFocus("set:serverdl"), detail = "Streamed titles being saved to the server")
                        }
                        ListRow("Server Settings", { nav.push(Route.ServerSettings) }, Modifier.rememberFocus("set:server"), detail = "Name, metadata, streaming and defaults")
                    }
                }
                FormSection("This TV", "Wren TV for Android TV $appVersion") {
                    if (state.account != null) {
                        ListRow("Switch Server", { state.switchServer() }, Modifier.rememberFocus("set:switch"),
                            detail = "Your other servers, or ones shared with you")
                    }
                    ListRow("Sign Out", { panel = SettingsPanel.SignOut }, Modifier.rememberFocus("set:signout"),
                        detail = "Downloads stay on this TV")
                }
            }
        }
    }

    when (panel) {
        null -> {}
        SettingsPanel.Audio -> ChoicePanel("Audio Language", languageChoices("File's default track"), prefs?.audioLanguage ?: "", { panel = null }) {
            prefs?.let { p -> save(p.copy(audioLanguage = it)) }
        }
        SettingsPanel.Subtitles -> ChoicePanel("Subtitles", languageChoices("Off"), prefs?.subtitleLanguage ?: "", { panel = null }) {
            prefs?.let { p -> save(p.copy(subtitleLanguage = it)) }
        }
        SettingsPanel.Watched -> ChoicePanel("Counts as Watched At", WatchedPercents.map { Choice(it, "$it%") }, prefs?.watchedPercent, { panel = null }) {
            prefs?.let { p -> save(p.copy(watchedPercent = it)) }
        }
        SettingsPanel.ContinueAfter -> ChoicePanel("Add to Continue Watching After", ContinueAfters.map { Choice(it.first, it.second) }, prefs?.continueAfter, { panel = null }) {
            prefs?.let { p -> save(p.copy(continueAfter = it)) }
        }
        SettingsPanel.PosterSize -> ChoicePanel("Poster Size", posterSizes, state.posterSize, { panel = null }) { state.posterSize = it }
        SettingsPanel.Screensaver -> ChoicePanel("Screensaver", screensaverChoices(collections), state.screensaver, { panel = null }) {
            state.screensaver = it
        }
        SettingsPanel.SignOut -> ConfirmPanel("Sign out?", "You'll need to sign in again to watch from ${server?.name ?: "this server"}.", "Sign Out", { panel = null }, destructive = true) {
            state.signOut()
        }
    }
}

private fun screensaverChoices(collections: List<WrenCollection>?): List<Choice<String>> =
    listOf(Choice("auto", "Library Highlights", "What's popular, new and on Home")) +
        collections.orEmpty().filter { it.active != false }.map { Choice("collection:${it.id}", it.name, it.group ?: "Collection") } +
        Choice("clock", "Clock Only")

private fun screensaverLabel(value: String, collections: List<WrenCollection>?): String = when {
    value == "clock" -> "Clock Only"
    value.startsWith("collection:") -> collections?.firstOrNull { it.id == value.removePrefix("collection:") }?.name ?: "A collection"
    else -> "Library Highlights"
}

private fun roleName(role: String?, admin: Boolean): String? = when {
    role == "owner" -> "Owner"
    admin || role == "admin" -> "Admin"
    else -> null
}

/** Where the TV reaches the server, in words: the address itself means nothing on the couch. */
private fun connectionLabel(url: String?): String? {
    val host = url?.let { runCatching { java.net.URI(it).host }.getOrNull() } ?: return null
    // Relay-style names carry the LAN address dashed ("192-168-1-5.<id>.wrentv.net").
    val ip = host.substringBefore('.').replace('-', '.').takeIf { host.contains(".wrentv.net") } ?: host
    val local = ip.startsWith("192.168.") || ip.startsWith("10.") || ip == "localhost" ||
        Regex("""^172\.(1[6-9]|2\d|3[01])\.""").containsMatchIn(ip) || host.endsWith(".local")
    return if (local) "Connected on your home network" else "Connected over the internet"
}

/**
 * Linking Simkl or Trakt: what's watched here is sent there, and its history
 * imported. The code is entered on a phone. Hidden when the server has no
 * app for the tracker.
 */
@Composable
private fun TrackerSection(tracker: Tracker) {
    val state = LocalAppState.current
    val notices = LocalNotices.current
    var st by remember { mutableStateOf<TrackerStatus?>(null) }
    var tick by remember { mutableIntStateOf(0) }
    LaunchedEffect(tick) {
        st = runCatching { state.api?.tracker(tracker) }.getOrNull()
        // Waiting on approval or an import: check again shortly.
        if (st?.busy == true) { delay(3000); tick++ }
    }
    val s = st ?: return
    if (!s.available && !s.linked) return
    fun act(f: suspend () -> Unit) {
        state.scope.launch { runCatching { f() }.onFailure { notices.show("${tracker.label}: ${it.message}") }; tick++ }
    }
    val a = s.account
    val pending = s.pending
    when {
        s.linked && a != null -> FormSection(tracker.label, when {
            s.importing || a.importStatus == "running" -> "Importing your history…"
            a.importStatus == "done" -> "Last import: ${a.importMovies} movies and ${a.importEpisodes} episodes marked watched."
            a.importStatus.isNotEmpty() -> "Import failed: ${a.importStatus}"
            else -> null
        }) {
            ListRow("Linked to ${a.username.ifEmpty { tracker.label }}", {}, Modifier.rememberFocus("set:${tracker.id}"))
            ListRow("Send what I watch to ${tracker.label}", { act { state.api?.trackerScrobble(tracker, !a.scrobble) } }, Modifier.rememberFocus("set:${tracker.id}-scrobble"),
                value = if (a.scrobble) "On" else "Off")
            ListRow("Import history again", { act { state.api?.trackerImport(tracker) } }, Modifier.rememberFocus("set:${tracker.id}-import"), enabled = !s.importing)
            ListRow("Disconnect", { act { state.api?.trackerUnlink(tracker) } }, Modifier.rememberFocus("set:${tracker.id}-unlink"))
        }
        pending != null && pending.error.isNullOrEmpty() -> FormSection(tracker.label, "Waiting for you to approve it on ${tracker.label}…") {
            Row(Modifier.fillMaxWidth().padding(18.dp), horizontalArrangement = Arrangement.spacedBy(28.dp), verticalAlignment = Alignment.CenterVertically) {
                QrCode(pending.urlComplete ?: pending.url, size = 160.dp)
                Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text("On your phone, open ${pending.url.removePrefix("https://")} and enter", style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
                    Text(pending.userCode, style = MaterialTheme.typography.displaySmall, color = Wren.text)
                }
            }
            ListRow("Cancel", { act { state.api?.trackerCancelLink(tracker) } }, Modifier.rememberFocus("set:${tracker.id}-cancel"))
        }
        else -> FormSection(tracker.label, pending?.error ?: "Link your ${tracker.label} account to import what you've already watched and keep ${tracker.label} up to date as you watch here.") {
            ListRow("Connect ${tracker.label}", { act { state.api?.trackerLink(tracker) } }, Modifier.rememberFocus("set:${tracker.id}-link"))
        }
    }
}
