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
import com.wrentv.tv.data.OfflineItem
import com.wrentv.tv.data.UserPrefs
import com.wrentv.tv.data.languageName
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

private enum class SettingsPanel { Audio, Subtitles, PosterSize, SignOut }

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
                }
                FormSection("Browsing") {
                    ListRow("Layouts", { nav.push(Route.Layouts) }, Modifier.rememberFocus("set:layouts"),
                        detail = if (state.isAdmin) "Rows on Home and each library, yours and everyone's" else "The rows on Home and each library")
                }
                FormSection("Sound", "Kept on this TV.") {
                    ListRow("Interface Sounds", { state.uiSounds = !state.uiSounds }, Modifier.rememberFocus("set:sounds"),
                        detail = "A click as you move around", value = if (state.uiSounds) "On" else "Off")
                    ListRow("Theme Music", { state.themeMusic = !state.themeMusic }, Modifier.rememberFocus("set:theme"),
                        detail = "Plays a show's theme on its page", value = if (state.themeMusic) "On" else "Off")
                    ListRow("Poster Size", { panel = SettingsPanel.PosterSize }, Modifier.rememberFocus("set:posters"),
                        detail = "How big titles are on Home and as you browse",
                        value = posterSizes.firstOrNull { it.value == state.posterSize }?.label ?: "Medium")
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
        SettingsPanel.PosterSize -> ChoicePanel("Poster Size", posterSizes, state.posterSize, { panel = null }) { state.posterSize = it }
        SettingsPanel.SignOut -> ConfirmPanel("Sign out?", "You'll need to sign in again to watch from ${server?.name ?: "this server"}.", "Sign Out", { panel = null }, destructive = true) {
            state.signOut()
        }
    }
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
