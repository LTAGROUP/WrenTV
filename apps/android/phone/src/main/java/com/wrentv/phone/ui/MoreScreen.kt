package com.wrentv.phone.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.Bookmark
import androidx.compose.material.icons.rounded.CalendarMonth
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.Collections
import androidx.compose.material.icons.rounded.Dashboard
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.LiveTv
import androidx.compose.material.icons.rounded.Person
import androidx.compose.material.icons.rounded.SwapHoriz
import androidx.compose.material.icons.rounded.Tune
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.rounded.Tv
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardCapitalization
import com.wrentv.tv.data.AppState
import com.wrentv.tv.data.ContinueAfters
import com.wrentv.tv.data.SimklStatus
import com.wrentv.tv.data.UserPrefs
import com.wrentv.tv.data.WatchedPercents
import com.wrentv.tv.data.continueAfterLabel
import com.wrentv.tv.data.languageName
import com.wrentv.tv.data.normalizeTvCode
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Everything that isn't a tab: lists, settings and the account. */
@Composable
fun MoreScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    var confirmSignOut by remember { mutableStateOf(false) }
    Page("More") {
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(bottom = 24.dp)) {
            if (state.liveTv) NavRow(Icons.Rounded.LiveTv, "Live TV") { nav.push(Route.LiveTv) }
            NavRow(Icons.Rounded.Bookmark, "Watchlist") { nav.push(Route.Watchlist) }
            NavRow(Icons.Rounded.Download, "Downloads", state.downloads.items.size.takeIf { it > 0 }?.toString()) { nav.push(Route.Downloads) }
            NavRow(Icons.Rounded.Collections, "Collections") { nav.push(Route.Collections) }
            if (state.canStream || state.libraries.any { it.type == "shows" }) NavRow(Icons.Rounded.CalendarMonth, "Calendar") { nav.push(Route.Calendar) }
            NavRow(Icons.Rounded.Dashboard, "Layouts") { nav.push(Route.Layouts) }
            NavRow(Icons.Rounded.Tune, "Settings") { nav.push(Route.Preferences) }
            NavRow(Icons.Rounded.Tv, "Sign in a TV") { nav.push(Route.LinkTv) }
            SectionHeader("Account")
            state.accountEmail?.let { NavRow(Icons.Rounded.Person, it, onClick = null) }
            state.user?.let { u -> if (state.accountEmail == null) NavRow(Icons.Rounded.Person, u.username, onClick = null) }
            state.server?.let { NavRow(Icons.Rounded.Dns, it.name, state.serverUrl?.host, onClick = null) }
            if (state.account != null) NavRow(Icons.Rounded.SwapHoriz, "Switch Server") { state.switchServer() }
            NavRow(Icons.AutoMirrored.Rounded.Logout, "Sign Out", tint = Wren.ember) { confirmSignOut = true }
        }
    }
    if (confirmSignOut) AlertDialog(
        { confirmSignOut = false },
        confirmButton = { TextButton({ confirmSignOut = false; state.signOut() }) { Text("Sign Out", color = Wren.ember) } },
        dismissButton = { TextButton({ confirmSignOut = false }) { Text("Cancel") } },
        title = { Text("Sign out?") },
        text = { Text("Downloads stay on this phone.") },
        containerColor = Wren.surface,
    )
}

private val languages = listOf("en", "es", "fr", "de", "it", "pt", "ja", "ko", "zh", "hi", "ru", "ar", "nl", "sv", "no", "da", "fi", "pl", "tr", "el",
    "he", "th", "vi", "id", "cs", "hu", "ro", "uk", "ta", "te", "tl", "fa")

/** Which audio and subtitle languages play by default. Saved on the server, so every device shares them. */
@Composable
fun PreferencesScreen() {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var prefs by remember { mutableStateOf<UserPrefs?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) { runCatching { state.api?.prefs() }.onSuccess { prefs = it }.onFailure { error = it.message } }
    fun save(p: UserPrefs) {
        val old = prefs
        prefs = p
        scope.launch { runCatching { state.api?.setPrefs(p) }.onFailure { prefs = old; error = it.message } }
    }
    Page("Settings") {
        val p = prefs
        if (p == null) { if (error != null) EmptyState(Icons.Rounded.Tune, "Couldn't load", error) else Loading(); return@Page }
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            SectionHeader("Languages")
            LanguagePicker("Audio", p.audioLanguage.ifEmpty { null }, "Original") { save(p.copy(audioLanguage = it ?: "")) }
            LanguagePicker("Subtitles", p.subtitleLanguage?.ifEmpty { null }, "Off") { save(p.copy(subtitleLanguage = it ?: "")) }
            SwitchRow("Forced subtitles", "With subtitles off, still show foreign-language lines and signs", p.forcedSubtitles) { save(p.copy(forcedSubtitles = it)) }
            SwitchRow("Subtitles with foreign audio", "When a title plays in another language, show subtitles in your audio language", p.foreignAudioSubtitles) {
                save(p.copy(foreignAudioSubtitles = it))
            }
            Text("These apply on every device signed in to your account. You can still change tracks while watching.",
                Modifier.fillMaxWidth().padding(horizontal = metrics().gutter, vertical = 8.dp), color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
            error?.let { Text(it, Modifier.padding(horizontal = metrics().gutter), color = Wren.ember, style = MaterialTheme.typography.bodySmall) }
            SectionHeader("Watching")
            ChoicePicker("Counts as watched at", WatchedPercents.map { it to "$it%" }, p.watchedPercent) { save(p.copy(watchedPercent = it)) }
            ChoicePicker("Add to Continue Watching after", ContinueAfters, p.continueAfter, continueAfterLabel(p.continueAfter)) { save(p.copy(continueAfter = it)) }
            Text("How far in a movie or episode counts as watched, and how long you watch before it's on Continue Watching.",
                Modifier.fillMaxWidth().padding(horizontal = metrics().gutter, vertical = 8.dp), color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
            SectionHeader("Browsing")
            PosterSizePicker()
            SimklSection()
        }
    }
}

/** A setting picked from a short list. */
@Composable
private fun <T> ChoicePicker(title: String, choices: List<Pair<T, String>>, value: T, label: String? = null, pick: (T) -> Unit) {
    var open by remember { mutableStateOf(false) }
    androidx.compose.foundation.layout.Box {
        NavRow(null, title, label ?: choices.firstOrNull { it.first == value }?.second) { open = true }
        DropdownMenu(open, { open = false }) {
            choices.forEach { (v, l) ->
                DropdownMenuItem({ Text(l) }, { open = false; pick(v) }, trailingIcon = { if (v == value) Icon(Icons.Rounded.Check, null) })
            }
        }
    }
}

/** Linking Simkl: what's watched here is sent there, and its history imported. Hidden when the server has no Simkl app. */
@Composable
private fun SimklSection() {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    val uri = LocalUriHandler.current
    var st by remember { mutableStateOf<SimklStatus?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var tick by remember { mutableIntStateOf(0) }
    LaunchedEffect(tick) {
        st = runCatching { state.api?.simkl() }.getOrNull()
        // Waiting on approval or an import: check again shortly.
        if (st?.busy == true) { delay(3000); tick++ }
    }
    val s = st ?: return
    if (!s.available && !s.linked) return
    fun act(f: suspend () -> Unit) {
        scope.launch { runCatching { f() }.onFailure { error = it.message }; tick++ }
    }
    SectionHeader("Simkl")
    val a = s.account
    val pending = s.pending
    when {
        s.linked && a != null -> {
            NavRow(null, "Linked to ${a.username.ifEmpty { "your Simkl account" }}", onClick = null)
            NavRow(null, "Send what I watch to Simkl", if (a.scrobble) "On" else "Off") { act { state.api?.simklScrobble(!a.scrobble) } }
            NavRow(null, "Import history again", when {
                s.importing || a.importStatus == "running" -> "Importing…"
                a.importStatus == "done" -> "${a.importMovies} movies, ${a.importEpisodes} episodes"
                a.importStatus.isNotEmpty() -> "Failed"
                else -> null
            }) { if (!s.importing) act { state.api?.simklImport() } }
            NavRow(null, "Disconnect Simkl", tint = Wren.ember) { act { state.api?.simklUnlink() } }
        }
        pending != null && pending.error.isNullOrEmpty() -> {
            NavRow(null, "Open ${pending.url.removePrefix("https://").removePrefix("http://")}", pending.userCode) {
                runCatching { uri.openUri(pending.urlComplete ?: pending.url) }
            }
            Text("Enter the code on Simkl to approve it. Waiting…", Modifier.padding(horizontal = metrics().gutter, vertical = 4.dp),
                color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
            NavRow(null, "Cancel") { act { state.api?.simklCancelLink() } }
        }
        else -> {
            NavRow(null, "Connect Simkl") {
                act { state.api?.simklLink()?.let { p -> runCatching { uri.openUri(p.urlComplete ?: p.url) } } }
            }
            Text(pending?.error ?: "Import what you've already watched, and keep Simkl up to date as you watch here.",
                Modifier.padding(horizontal = metrics().gutter, vertical = 4.dp), color = if (pending?.error != null) Wren.ember else Wren.secondary,
                style = MaterialTheme.typography.bodySmall)
        }
    }
    error?.let { Text(it, Modifier.padding(horizontal = metrics().gutter), color = Wren.ember, style = MaterialTheme.typography.bodySmall) }
}

/** Signs in a TV showing a code: to this account, or to this server as you. */
@Composable
fun LinkTvScreen() {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var code by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var pending by remember { mutableStateOf<AppState.TvCode?>(null) }
    var linked by remember { mutableStateOf<String?>(null) }
    Page("Sign in a TV") {
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = metrics().gutter, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)) {
            val done = linked
            if (done != null) {
                Text("$done is signed in as ${state.user?.displayName?.ifEmpty { null } ?: state.user?.username ?: "you"}.", color = Color.White)
                TextButton({ linked = null; code = "" }) { Text("Sign in another TV", color = Wren.ember) }
                return@Column
            }
            Text("On the TV, choose Sign in with your phone, then type the code it shows.", color = Wren.secondary)
            OutlinedTextField(code, { code = it.uppercase(); error = null }, Modifier.fillMaxWidth(), singleLine = true,
                placeholder = { Text("ABCD-EFGH") }, textStyle = MaterialTheme.typography.titleLarge.copy(fontFamily = FontFamily.Monospace),
                keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Characters, autoCorrectEnabled = false),
                colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = Wren.ember, cursorColor = Color.White))
            Button({
                busy = true
                scope.launch {
                    runCatching { state.lookUpTvCode(code) }.onSuccess { pending = it }.onFailure { error = it.message }
                    busy = false
                }
            }, enabled = !busy && normalizeTvCode(code).length == 8, colors = ButtonDefaults.buttonColors(containerColor = Wren.ember)) { Text("Continue") }
            error?.let { Text(it, color = Wren.ember, style = MaterialTheme.typography.bodySmall) }
        }
    }
    pending?.let { p ->
        AlertDialog(
            { pending = null },
            confirmButton = {
                TextButton({
                    pending = null
                    scope.launch { runCatching { state.approveTvCode(p) }.onSuccess { linked = p.device; code = "" }.onFailure { error = it.message } }
                }) { Text("Sign In", color = Wren.ember) }
            },
            dismissButton = { TextButton({ pending = null }) { Text("Cancel") } },
            title = { Text("Sign in this TV?") },
            text = {
                Text(if (p.account) "${p.device} will be signed in to your Wren TV account (${state.accountEmail ?: "this account"}) and open your servers."
                    else "${p.device} will be signed in to ${state.server?.name ?: "this server"} as ${state.user?.displayName?.ifEmpty { null } ?: state.user?.username ?: "you"}.")
            },
            containerColor = Wren.surface,
        )
    }
}

/** How big posters and tiles are, saved to the account so every device matches. */
@Composable
private fun PosterSizePicker() {
    val state = LocalAppState.current
    var open by remember { mutableStateOf(false) }
    val sizes = listOf("small" to "Small", "medium" to "Medium", "large" to "Large")
    androidx.compose.foundation.layout.Box {
        NavRow(null, "Poster size", sizes.firstOrNull { it.first == state.posterSize }?.second ?: "Medium") { open = true }
        DropdownMenu(open, { open = false }) {
            sizes.forEach { (v, label) ->
                DropdownMenuItem({ Text(label) }, { open = false; state.posterSize = v }, trailingIcon = { if (state.posterSize == v) Icon(Icons.Rounded.Check, null) })
            }
        }
    }
}

/** A setting that's on or off, with a line saying what it does. */
@Composable
private fun SwitchRow(title: String, detail: String, checked: Boolean, change: (Boolean) -> Unit) {
    androidx.compose.foundation.layout.Row(
        Modifier.fillMaxWidth().clickable { change(!checked) }.padding(horizontal = 20.dp, vertical = 10.dp),
        verticalAlignment = androidx.compose.ui.Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(detail, color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
        }
        androidx.compose.material3.Switch(checked, change)
    }
}

@Composable
private fun LanguagePicker(title: String, code: String?, none: String, pick: (String?) -> Unit) {
    var open by remember { mutableStateOf(false) }
    androidx.compose.foundation.layout.Box {
        NavRow(null, title, languageName(code) ?: none) { open = true }
        DropdownMenu(open, { open = false }) {
            DropdownMenuItem({ Text(none) }, { open = false; pick(null) }, trailingIcon = { if (code == null) Icon(Icons.Rounded.Check, null) })
            languages.forEach { l ->
                DropdownMenuItem({ Text(languageName(l) ?: l) }, { open = false; pick(l) }, trailingIcon = { if (code == l) Icon(Icons.Rounded.Check, null) })
            }
        }
    }
}
