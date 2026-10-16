package com.wrentv.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import com.wrentv.tv.data.Item
import com.wrentv.tv.ui.ListRow
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.LocalNotices
import com.wrentv.tv.ui.Panel
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.playTarget
import kotlinx.coroutines.android.awaitFrame
import kotlinx.coroutines.launch

/** Holds the item a long press opened the menu for. */
class ItemMenuState {
    var item by mutableStateOf<Item?>(null)
    fun open(item: Item) { this.item = item }
}

@Composable
fun rememberItemMenu(): ItemMenuState {
    val m = remember { ItemMenuState() }
    ItemMenu(m)
    return m
}

/** What a long press on a card offers: details, play from the start, watched. */
@Composable
private fun ItemMenu(m: ItemMenuState) {
    val item = m.item ?: return
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val notices = LocalNotices.current
    val close = { m.item = null }
    Panel(close, if (item.kind == "episode") "${item.showTitle ?: item.title} · ${item.episodeLabel}" else item.displayTitle) {
        val first = remember { FocusRequester() }
        LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }
        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            ListRow("Details", { close(); nav.push(Route.Detail(item.id, item)) }, Modifier.focusRequester(first))
            if (item.isPlayable) {
                ListRow(if (item.inProgress) "Resume" else "Play", { close(); nav.play(item.playTarget()) })
                if (item.inProgress) ListRow("Play from Beginning", { close(); nav.play(item.playTarget(fromStart = true)) })
            }
            if (item.kind == "episode" && item.showId != null) {
                ListRow("Go to Show", { close(); nav.push(Route.Detail(item.showId)) })
            }
            if (item.isPlayable || item.kind == "show" || item.kind == "season") {
                ListRow(if (item.isWatched) "Mark as Unwatched" else "Mark as Watched", {
                    close()
                    // The menu is gone by now, so this runs in the app's scope.
                    state.scope.launch {
                        try {
                            state.api?.setWatched(item.id, !item.isWatched)
                            state.refreshTick++
                        } catch (e: Exception) {
                            notices.show(e.message)
                        }
                    }
                })
            }
        }
    }
}
