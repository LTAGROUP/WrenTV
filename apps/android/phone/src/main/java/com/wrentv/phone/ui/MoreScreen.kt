package com.wrentv.phone.ui

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
import com.wrentv.tv.data.UserPrefs
import com.wrentv.tv.data.languageName
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
            Text("These apply on every device signed in to your account. You can still change tracks while watching.",
                Modifier.fillMaxWidth().padding(horizontal = metrics().gutter, vertical = 8.dp), color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
            error?.let { Text(it, Modifier.padding(horizontal = metrics().gutter), color = Wren.ember, style = MaterialTheme.typography.bodySmall) }
            SectionHeader("Browsing")
            PosterSizePicker()
        }
    }
}

/** How big posters and tiles are, on this phone. */
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
