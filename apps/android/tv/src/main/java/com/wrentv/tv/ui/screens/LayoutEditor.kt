package com.wrentv.tv.ui.screens

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.data.LayoutConfig
import com.wrentv.tv.data.LayoutEntry
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.ConfirmPanel
import com.wrentv.tv.ui.FormSection
import com.wrentv.tv.ui.ListRow
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.LocalNotices
import com.wrentv.tv.ui.PageCache
import com.wrentv.tv.ui.ReportScroll
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.rememberFocus
import kotlinx.coroutines.launch

/** The pages whose rows can be arranged. */
@Composable
fun LayoutsScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val pages = buildList {
        add("home" to "Home")
        if (state.canStream) {
            add("movies" to "Movies")
            add("shows" to "TV Shows")
        }
        state.libraries.forEach { add("library:${it.id}" to it.name) }
    }
    EditorColumn("Layouts") {
        FormSection("Pages", "Choose which rows each page shows, and in what order." +
            if (state.isAdmin) " You can also set the layout everyone starts with." else "") {
            pages.forEachIndexed { i, (scope, title) ->
                ListRow(title, { nav.push(Route.LayoutEditor(scope, title)) }, Modifier.rememberFocus("layouts:$scope", default = i == 0))
            }
        }
    }
}

private enum class EditorPanel { Add, Reset }

/**
 * One page's rows. Changes save as they're made, to the user's own
 * layout or, for admins who switch to it, everyone's default.
 */
@Composable
fun LayoutEditorScreen(scope: String, title: String) {
    val state = LocalAppState.current
    val notices = LocalNotices.current
    var everyone by rememberSaveable { mutableStateOf(false) }
    var config by remember { mutableStateOf<LayoutConfig?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var picked by remember { mutableStateOf<LayoutEntry?>(null) }
    var panel by remember { mutableStateOf<EditorPanel?>(null) }

    suspend fun load() {
        try {
            config = state.api?.layoutConfig(scope, everyone)
            error = null
        } catch (e: Exception) {
            error = e.message
        }
    }
    LaunchedEffect(scope, everyone) { config = null; load() }

    fun save(entries: List<LayoutEntry>) {
        val before = config ?: return
        config = before.copy(entries = entries, custom = true)
        state.scope.launch {
            try {
                state.api?.setLayout(scope, everyone, entries)
                // The page reloads with the new rows next time it opens.
                PageCache.values.remove("layout:$scope")
                state.refreshTick++
            } catch (e: Exception) {
                config = before
                notices.show("Couldn't save: ${e.message}")
            }
        }
    }

    fun move(e: LayoutEntry, by: Int) {
        val list = config?.entries?.toMutableList() ?: return
        val i = list.indexOfFirst { it.key == e.key }
        val j = i + by
        if (i < 0 || j !in list.indices) return
        list.add(j, list.removeAt(i))
        save(list)
    }

    EditorColumn(title) {
        val c = config
        if (state.isAdmin) {
            FormSection("Editing") {
                ListRow("My layout", { everyone = false }, Modifier.rememberFocus("editor:me", default = true), checked = !everyone,
                    detail = "Only you see these changes")
                ListRow("Default for everyone", { everyone = true }, Modifier.rememberFocus("editor:default"), checked = everyone,
                    detail = "What users see until they arrange their own")
            }
        }
        when {
            c == null && error != null -> Text(error ?: "", color = Wren.ember)
            c == null -> Loading()
            else -> {
                val inherited = when {
                    c.custom -> null
                    !everyone && c.hasDefault -> "Showing the server's default. Changing anything makes it your own."
                    else -> "Showing Wren TV's built-in layout."
                }
                FormSection("Rows", inherited ?: "Select a row to move, hide or remove it.") {
                    c.entries.forEachIndexed { i, e ->
                        // Keyed, so focus moves with a row as it's moved.
                        key(e.key) {
                            ListRow(e.title, { picked = e }, Modifier.rememberFocus("editor:${e.key}", default = !state.isAdmin && i == 0),
                                detail = e.detail, checked = !e.hidden, value = if (e.hidden) "Hidden" else null)
                        }
                    }
                    ListRow("Add a Row…", { panel = EditorPanel.Add }, Modifier.rememberFocus("editor:add"))
                }
                if (c.custom) {
                    FormSection("Reset") {
                        ListRow(if (everyone || !c.hasDefault) "Back to the Built-in Layout" else "Use the Default Layout", { panel = EditorPanel.Reset }, Modifier.rememberFocus("editor:reset"))
                    }
                }
            }
        }
    }

    val c = config
    picked?.let { e ->
        val entries = c?.entries.orEmpty()
        val i = entries.indexOfFirst { it.key == e.key }
        val actions = buildList {
            if (i > 0) add(Choice("up", "Move Up"))
            if (i in 0 until entries.lastIndex) add(Choice("down", "Move Down"))
            if (i > 1) add(Choice("top", "Move to Top"))
            add(Choice("toggle", if (e.hidden) "Show" else "Hide"))
            add(Choice("remove", "Remove", "Add it back any time"))
        }
        ChoicePanel(e.title, actions, null, { picked = null }) { action ->
            when (action) {
                "up" -> move(e, -1)
                "down" -> move(e, 1)
                "top" -> move(e, -i)
                "toggle" -> save(entries.map { if (it.key == e.key) it.copy(hidden = !it.hidden) else it })
                "remove" -> save(entries.filter { it.key != e.key })
            }
        }
    }
    when (panel) {
        null -> {}
        EditorPanel.Add -> {
            val have = c?.entries.orEmpty().map { it.key }.toSet()
            val options = c?.options.orEmpty().filter { it.key !in have }
            ChoicePanel("Add a Row", options.map { Choice(it.key, it.title, it.detail) }, null, { panel = null }) { key ->
                options.firstOrNull { it.key == key }?.let { save(c?.entries.orEmpty() + it) }
            }
        }
        EditorPanel.Reset -> ConfirmPanel(
            "Reset $title?",
            if (everyone) "Everyone without their own layout goes back to Wren TV's built-in rows." else "Your changes to $title are dropped.",
            "Reset", { panel = null }, destructive = true,
        ) {
            state.scope.launch {
                try {
                    state.api?.resetLayout(scope, everyone)
                    PageCache.values.remove("layout:$scope")
                    state.refreshTick++
                    load()
                } catch (e: Exception) {
                    notices.show("Couldn't reset: ${e.message}")
                }
            }
        }
    }
}

@Composable
private fun EditorColumn(title: String, content: @Composable () -> Unit) {
    val list = rememberLazyListState()
    ReportScroll(list)
    LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(top = TopBarSpace, bottom = 48.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        item("header") {
            Text(title, style = MaterialTheme.typography.displaySmall, color = Wren.text, modifier = Modifier.width(860.dp).padding(top = 28.dp, bottom = 24.dp))
        }
        item("body") { Column(Modifier.width(860.dp)) { content() } }
    }
}
