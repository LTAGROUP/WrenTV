package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.BookmarkAdd
import androidx.compose.material.icons.rounded.BookmarkAdded
import androidx.compose.material.icons.rounded.CellTower
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.DownloadDone
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Replay
import androidx.compose.material.icons.rounded.Theaters
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.foundation.gestures.LocalBringIntoViewSpec
import androidx.compose.foundation.gestures.BringIntoViewSpec
import androidx.compose.ui.layout.onSizeChanged
import com.wrentv.tv.player.ThemeMusic
import androidx.compose.ui.platform.LocalContext
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import com.wrentv.tv.data.CastMember
import com.wrentv.tv.data.Extras
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.ItemDetail
import com.wrentv.tv.data.MediaFileView
import com.wrentv.tv.data.OfflineItem
import com.wrentv.tv.data.Ratings
import com.wrentv.tv.data.UserPrefs
import com.wrentv.tv.data.WrenCollection
import com.wrentv.tv.data.languageName
import com.wrentv.tv.ui.ArtCard
import com.wrentv.tv.ui.Badge
import com.wrentv.tv.ui.Chip
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.ConfirmPanel
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.ListRow
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.LocalNotices
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.Panel
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.PlayTarget
import com.wrentv.tv.ui.ProgressStrip
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.RoundButton
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.SectionTitle
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.TitleCard
import com.wrentv.tv.ui.ToggleRow
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.formatBytes
import com.wrentv.tv.ui.formatDuration
import com.wrentv.tv.ui.playTarget
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import com.wrentv.tv.ui.timeLeft
import kotlinx.coroutines.launch

@Composable
fun DetailScreen(itemId: String, seed: Item?) {
    val state = LocalAppState.current
    val detail = rememberLoaded<ItemDetail>("item:$itemId", state.refreshTick) { state.api?.item(itemId) }
    val extras = rememberLoaded<Extras>("extras:$itemId") { state.api?.extras(itemId) }
    val prefs = rememberLoaded<UserPrefs>("prefs") { state.api?.prefs() }
    val item = detail.value?.item ?: seed
    if (item == null) {
        if (detail.error != null) EmptyState("Couldn't load", detail.error, Modifier.padding(top = 120.dp), Icons.Rounded.WifiOff) else Loading(Modifier.padding(top = 120.dp))
        return
    }
    DetailPage(item, detail.value, extras.value, prefs.value)
}

/** Which panel is open over the page. */
private sealed interface DetailPanel {
    data object Version : DetailPanel
    data object Collections : DetailPanel
    data object StreamAdmin : DetailPanel
    data object ForgetStreams : DetailPanel
    data class Download(val item: Item) : DetailPanel
    data class Episode(val item: Item) : DetailPanel
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun DetailPage(item: Item, detail: ItemDetail?, extras: Extras?, prefs: UserPrefs?) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val notices = LocalNotices.current
    var versionId by rememberSaveable(item.id) { mutableStateOf<String?>(null) }
    var panel by remember { mutableStateOf<DetailPanel?>(null) }
    val list = rememberLazyListState()
    val show = detail?.show ?: if (item.kind == "show") item else null
    val backdrop = item.backdropPath ?: detail?.show?.backdropPath ?: item.stillPath
    ThemeEffect(item, show)

    fun play(target: Item, fromStart: Boolean = false) {
        val showTitle = if (target.kind == "episode") detail?.show?.title ?: (if (item.kind == "show") item.title else null) ?: target.showTitle else null
        nav.play(target.playTarget(showTitle, fromStart, if (target.id == item.id) versionId else null))
    }

    fun toggleWatched(target: Item) {
        state.scope.launch {
            try {
                state.api?.setWatched(target.id, !target.isWatched)
                state.refreshTick++
            } catch (e: Exception) {
                notices.show(e.message)
            }
        }
    }

    Box(Modifier.fillMaxSize()) {
        // The backdrop stays put behind everything and dims as the page scrolls.
        val dim = if (list.firstVisibleItemIndex > 0) 0.35f else 1f
        RemoteImage(state.api?.imageUrl(backdrop, "w1280"), Modifier.fillMaxSize().alpha(dim))
        Box(
            Modifier.fillMaxSize()
                .background(Brush.verticalGradient(0f to Color.Transparent, 0.45f to Color.Black.copy(alpha = 0.55f), 0.8f to Wren.background))
                .background(Brush.horizontalGradient(0f to Color.Black.copy(alpha = 0.85f), 0.6f to Color.Transparent)),
        )
        // The TV default keeps whatever has focus at a fixed spot, so moving along the
        // buttons nudged the page up and down. This page scrolls only to bring
        // something on screen; the rows inside keep the default.
        val rows = LocalBringIntoViewSpec.current
        var heroHeight by remember { mutableIntStateOf(0) }
        var inHero by remember { mutableStateOf(false) }
        val spec = remember(list) { DetailScroll(list) { if (inHero) heroHeight else null } }
        // Ratings and tracks arrive after the page opens; the hero settles again as it grows.
        LaunchedEffect(heroHeight, inHero) {
            if (inHero) list.animateScrollToItem(0, spec.heroScroll(heroHeight, list.layoutInfo.viewportSize.height.toFloat()))
        }
        CompositionLocalProvider(LocalBringIntoViewSpec provides spec) {
        LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(bottom = 56.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
            item("hero") {
                Column(
                    Modifier.onSizeChanged { heroHeight = it.height }.onFocusChanged { inHero = it.hasFocus }
                        .padding(start = PageGutter, end = PageGutter, top = 150.dp).width(820.dp),
                ) {
                    TitleBlock(item, detail, nav)
                    Spacer(Modifier.height(14.dp))
                    // The ratings pills below carry the score; the star would repeat it.
                    val scored = extras != null && (item.kind == "movie" || item.kind == "show") && extras.ratings.hasAny
                    MetaLine(metaParts(item, star = !scored), listOfNotNull(item.contentRating?.takeIf { it.isNotEmpty() }) + currentFile(detail, versionId)?.badges.orEmpty())
                    if (extras != null && (item.kind == "movie" || item.kind == "show")) {
                        RatingsRow(extras.ratings, Modifier.padding(top = 12.dp))
                    }
                    item.tagline?.takeIf { it.isNotEmpty() }?.let {
                        Text(it, style = MaterialTheme.typography.titleMedium.copy(fontStyle = FontStyle.Italic), color = Wren.secondary, modifier = Modifier.padding(top = 12.dp))
                    }
                    item.overview?.takeIf { it.isNotEmpty() }?.let {
                        Text(it, style = MaterialTheme.typography.bodyLarge, color = Color.White.copy(alpha = 0.85f), maxLines = 4, overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.padding(top = 12.dp))
                    }
                    val files = detail?.files.orEmpty()
                    if (item.isPlayable && files.isNotEmpty()) {
                        TrackRow(files, versionId, prefs, { panel = DetailPanel.Version }, Modifier.padding(top = 16.dp))
                    }
                    Spacer(Modifier.height(22.dp))
                    Actions(item, detail, extras, versionId, ::play, ::toggleWatched) { panel = it }
                }
            }
            when {
                item.kind == "show" && !detail?.children.isNullOrEmpty() -> item("seasons") {
                    CompositionLocalProvider(LocalBringIntoViewSpec provides rows) { Seasons(item, detail!!, ::play) { panel = it } }
                }
                item.kind == "season" && detail != null -> item("episodes") {
                    CompositionLocalProvider(LocalBringIntoViewSpec provides rows) { EpisodeRow(detail.children.orEmpty(), ::play) { panel = DetailPanel.Episode(it) } }
                }
            }
            if (extras != null && (item.kind == "movie" || item.kind == "show")) {
                if (extras.cast.isNotEmpty()) item("cast") { CompositionLocalProvider(LocalBringIntoViewSpec provides rows) { CastShelf(extras.cast) } }
                if (extras.moreLikeThis.isNotEmpty()) item("more") {
                    CompositionLocalProvider(LocalBringIntoViewSpec provides rows) {
                        Shelf("More Like This") {
                            items(extras.moreLikeThis, key = { it.id }) { t -> TitleCard(t, Modifier.rememberFocus("more:${t.id}")) }
                        }
                    }
                }
            }
            val files = detail?.files.orEmpty()
            if (files.isNotEmpty()) item("files") { FileSection(files) }
        }
        }
    }

    when (val p = panel) {
        null -> {}
        DetailPanel.Version -> ChoicePanel(
            "Version",
            listOf(Choice<String?>(null, "Automatic", "The best one that plays without converting")) +
                detail?.files.orEmpty().map { Choice<String?>(it.id, versionName(it), fileSummary(it)) },
            versionId, { panel = null },
        ) { versionId = it }
        DetailPanel.Collections -> CollectionsPanel(item) { panel = null }
        DetailPanel.StreamAdmin -> Panel({ panel = null }, "Streaming") {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                ListRow("Download to Server", {
                    panel = null
                    state.scope.launch {
                        try {
                            val n = state.api?.serverDownload(item.id) ?: 0
                            notices.show(if (n > 1) "$n episodes queued for download to the server." else "Queued for download to the server.")
                        } catch (e: Exception) {
                            notices.show(e.message)
                        }
                    }
                }, Modifier.rememberFocus("sa:dl", default = true), detail = "Save the file so it no longer streams")
                ListRow("Find New Streams", { panel = DetailPanel.ForgetStreams }, detail = "Forget the saved streams and search the addons again")
            }
        }
        DetailPanel.ForgetStreams -> ConfirmPanel(
            "Find new streams?",
            "The streams saved for ${when (item.kind) { "show" -> "every episode of this show"; "season" -> "this season"; else -> "this title" }} are forgotten. The next play searches the addons again.",
            "Find New Streams", { panel = null },
        ) {
            state.scope.launch {
                runCatching { state.api?.forgetStreams(item.id) }
                state.refreshTick++
            }
        }
        is DetailPanel.Download -> DownloadPanel(p.item, show, if (p.item.id == item.id) versionId else null) { panel = null }
        is DetailPanel.Episode -> {
            val ep = p.item
            Panel({ panel = null }, "${ep.episodeLabel} · ${ep.title}") {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    ListRow(if (ep.inProgress) "Resume" else "Play", { panel = null; play(ep) }, Modifier.rememberFocus("ep:play", default = true))
                    if (ep.inProgress) ListRow("Play from Beginning", { panel = null; play(ep, fromStart = true) })
                    ListRow("Episode Details", { panel = null; nav.push(Route.Detail(ep.id, ep)) })
                    ListRow(if (ep.isWatched) "Mark as Unwatched" else "Mark as Watched", { panel = null; toggleWatched(ep) })
                    if (detail?.canDownload == true) {
                        val x = state.downloads.item(ep.id)
                        if (x != null) ListRow("Remove Download", { panel = null; state.downloads.remove(x.id) })
                        else ListRow("Download", { panel = null; state.downloads.add(ep, show, server = state.server?.name ?: "") })
                    }
                }
            }
        }
    }
}

/**
 * Scrolls only as far as it takes to show the target, keeping a band at the top
 * and a bigger one at the bottom (for the text under a card); doesn't move for
 * what's already inside. Something too tall for both bands gets them shrunk.
 *
 * While the hero has focus ([hero] gives its height), the page sits where it
 * opened instead: at the top, or only as far down as shows the buttons.
 */
@OptIn(ExperimentalFoundationApi::class)
private class DetailScroll(val list: LazyListState, val hero: () -> Int?) : BringIntoViewSpec {
    private val TOP = 0.06f
    private val BOTTOM = 0.16f

    /** Where the page sits with the hero focused: its bottom just above the bottom band. */
    fun heroScroll(height: Int, containerSize: Float): Int = (height - containerSize * (1 - BOTTOM)).coerceAtLeast(0f).toInt()

    override fun calculateScrollDistance(offset: Float, size: Float, containerSize: Float): Float {
        hero()?.let { h ->
            // Below the hero, the scroll isn't known exactly; head up until it is.
            return if (list.firstVisibleItemIndex == 0) (heroScroll(h, containerSize) - list.firstVisibleItemScrollOffset).toFloat() else -containerSize * 4
        }
        val room = (containerSize - size).coerceAtLeast(0f)
        val fit = (room / ((TOP + BOTTOM) * containerSize)).coerceAtMost(1f)
        val top = TOP * containerSize * fit
        val bottom = containerSize - BOTTOM * containerSize * fit
        val end = offset + size
        return when {
            offset >= top - 0.5f && end <= bottom + 0.5f -> 0f
            size > containerSize -> offset
            offset < top -> offset - top
            else -> end - bottom
        }
    }
}

/** The movie's or show's theme while its page (or a season's) is open. */
@Composable
private fun ThemeEffect(item: Item, show: Item?) {
    val state = LocalAppState.current
    val context = LocalContext.current
    val key = when (item.kind) {
        "movie" -> item.id
        "show", "season" -> show?.id ?: item.parentId?.takeIf { item.kind == "season" }
        else -> null
    }
    val api = state.api
    DisposableEffect(key, api, state.themeMusic) {
        if (key != null && api != null && state.themeMusic) ThemeMusic.play(context.applicationContext, api, key, item.id)
        onDispose { ThemeMusic.leave() }
    }
}

private fun currentFile(detail: ItemDetail?, versionId: String?): MediaFileView? {
    val files = detail?.files ?: return null
    return files.firstOrNull { it.id == versionId } ?: files.maxByOrNull { it.width }
}

private fun metaParts(item: Item, star: Boolean = true): List<String> = listOfNotNull(
    item.year?.toString(),
    item.runtimeMin?.takeIf { it > 0 }?.let { formatDuration(it * 60.0) },
    item.rating?.takeIf { star && it > 0 }?.let { "★ %.1f".format(it) },
    item.airDate?.takeIf { it.isNotEmpty() && item.kind == "episode" },
) + item.genres.orEmpty().take(3)

@Composable
private fun TitleBlock(item: Item, detail: ItemDetail?, nav: com.wrentv.tv.ui.Navigator) {
    val state = LocalAppState.current
    val logo = item.logoPath ?: detail?.show?.logoPath
    if (item.kind == "episode" || item.kind == "season") {
        detail?.show?.let { show ->
            Text(show.title, style = MaterialTheme.typography.headlineSmall, color = Wren.secondary)
            Spacer(Modifier.height(4.dp))
        }
        Text(
            if (item.kind == "episode") "${item.episodeLabel} — ${item.title}" else item.displayTitle,
            style = MaterialTheme.typography.displaySmall.copy(fontWeight = FontWeight.Bold), color = Wren.text, maxLines = 2, overflow = TextOverflow.Ellipsis,
        )
    } else if (logo != null) {
        AsyncImage(state.api?.imageUrl(logo, "w500"), item.title, Modifier.width(440.dp).height(150.dp),
            contentScale = ContentScale.Fit, alignment = Alignment.BottomStart)
    } else {
        Text(item.title, style = MaterialTheme.typography.displayMedium.copy(fontWeight = FontWeight.Bold), color = Wren.text, maxLines = 2, overflow = TextOverflow.Ellipsis)
    }
}

private val Ratings.hasAny: Boolean
    get() = (rottenTomatoes ?: 0) > 0 || (imdb ?: 0.0) > 0 || (metacritic ?: 0) > 0 || (tmdb ?: 0.0) > 0

/** IMDb, Rotten Tomatoes, Metacritic and TMDB scores as small pills. */
@Composable
private fun RatingsRow(r: Ratings, modifier: Modifier = Modifier) {
    val pills = buildList {
        r.rottenTomatoes?.takeIf { it > 0 }?.let { add((if (it >= 60) "🍅" else "🤢") to "$it%") }
        r.imdb?.takeIf { it > 0 }?.let { add("IMDb" to "%.1f".format(it)) }
        r.metacritic?.takeIf { it > 0 }?.let { add("MC" to "$it") }
        r.tmdb?.takeIf { it > 0 }?.let { add("TMDB" to "%.1f".format(it)) }
    }
    if (pills.isEmpty()) return
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        pills.forEach { (label, value) ->
            Row(
                Modifier.background(Color.White.copy(alpha = 0.1f), CircleShape).padding(horizontal = 12.dp, vertical = 5.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically,
            ) {
                if (label == "IMDb") {
                    Text(label, style = MaterialTheme.typography.labelSmall.copy(fontWeight = FontWeight.Black), color = Color.Black,
                        modifier = Modifier.background(Color(0xFFF5C518), RoundedCornerShape(3.dp)).padding(horizontal = 4.dp))
                } else {
                    Text(label, style = MaterialTheme.typography.labelSmall.copy(fontWeight = FontWeight.Bold), color = Wren.secondary)
                }
                Text(value, style = MaterialTheme.typography.labelLarge, color = Wren.text)
            }
        }
    }
}

/** What will play: VERSION · AUDIO · SUBTITLES, from the viewer's settings unless they pick a version. */
@Composable
private fun TrackRow(files: List<MediaFileView>, versionId: String?, prefs: UserPrefs?, pickVersion: () -> Unit, modifier: Modifier = Modifier) {
    val picked = files.firstOrNull { it.id == versionId }
    val best = picked ?: files.maxByOrNull { it.width }
    val quality = best?.badges?.firstOrNull() ?: ""
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
        val version = picked?.let(::versionName) ?: if (files.size > 1) (if (quality.isEmpty()) "Auto" else "Auto · $quality") else quality.ifEmpty { "Original" }
        if (files.size > 1) {
            Surface(
                onClick = pickVersion,
                modifier = Modifier.rememberFocus("track:version"),
                shape = ClickableSurfaceDefaults.shape(CircleShape),
                colors = ClickableSurfaceDefaults.colors(containerColor = Color.Transparent, contentColor = Wren.text,
                    focusedContainerColor = Color.White, focusedContentColor = Color.Black),
                scale = ClickableSurfaceDefaults.scale(focusedScale = 1.04f),
            ) {
                TrackLabel("Version", "$version  ▾", Modifier.padding(horizontal = 12.dp, vertical = 6.dp))
            }
        } else {
            TrackLabel("Version", version, Modifier.padding(horizontal = 12.dp, vertical = 6.dp))
        }
        TrackLabel("Audio", languageName(prefs?.audioLanguage) ?: "Default")
        Spacer(Modifier.width(12.dp))
        TrackLabel("Subtitles", languageName(prefs?.subtitleLanguage) ?: "Off")
    }
}

@Composable
private fun TrackLabel(title: String, value: String, modifier: Modifier = Modifier) {
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(title.uppercase(), style = MaterialTheme.typography.labelSmall.copy(fontWeight = FontWeight.Bold, letterSpacing = 1.sp),
            color = androidx.tv.material3.LocalContentColor.current.copy(alpha = 0.55f))
        Text(value, style = MaterialTheme.typography.titleSmall)
    }
}

private fun versionName(f: MediaFileView): String =
    f.label ?: ((f.badges.firstOrNull()?.let { "$it · " } ?: "") + formatBytes(f.size))

private fun fileSummary(f: MediaFileView): String = listOfNotNull(
    f.info?.video?.firstOrNull()?.let { v -> "${v.codec.uppercase()} ${v.width ?: 0}×${v.height ?: 0}${v.hdr?.let { " $it" } ?: ""}" },
    (f.info?.audio?.firstOrNull { it.default } ?: f.info?.audio?.firstOrNull())?.codecLabel,
    f.size.takeIf { it > 0 }?.let(::formatBytes),
).joinToString(" · ")

@Composable
private fun Actions(
    item: Item,
    detail: ItemDetail?,
    extras: Extras?,
    versionId: String?,
    play: (Item, Boolean) -> Unit,
    toggleWatched: (Item) -> Unit,
    open: (DetailPanel) -> Unit,
) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    // Room underneath for the focused button's label.
    Row(Modifier.padding(bottom = 40.dp), horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
        if (item.isPlayable) {
            val resume = item.inProgress
            val left = item.progress?.let(::timeLeft)
            PillButton(if (resume) left?.let { "Resume · $it" } ?: "Resume" else "Play", { play(item, false) },
                Modifier.rememberFocus("act:play", default = true), icon = Icons.Rounded.PlayArrow, primary = true)
            if (resume) PillButton("Start Over", { play(item, true) }, Modifier.rememberFocus("act:restart"), icon = Icons.Rounded.Replay)
        } else if (item.kind == "show" || item.kind == "season") {
            val next = detail?.nextEpisode ?: if (item.kind == "season") detail?.children?.firstOrNull { !it.isWatched } else null
            if (next != null) {
                PillButton("${if (next.inProgress) "Resume" else "Play"} S${next.seasonNumber}:E${next.episodeNumber}", { play(next, false) },
                    Modifier.rememberFocus("act:play", default = true), icon = Icons.Rounded.PlayArrow, primary = true)
            }
        }
        extras?.trailer?.let { t ->
            RoundButton(Icons.Rounded.Theaters, "Play Trailer", { nav.push(Route.Trailer(t.youtubeKey, item.title)) }, Modifier.rememberFocus("act:trailer"))
        }
        if (detail?.onWatchlist != null) {
            // Flips at once; the server catches up behind it.
            var listed by remember(item.id, detail.onWatchlist) { mutableStateOf(detail.onWatchlist == true) }
            val notices = LocalNotices.current
            RoundButton(
                if (listed) Icons.Rounded.BookmarkAdded else Icons.Rounded.BookmarkAdd,
                if (listed) "On Watchlist · Remove" else "Add to Watchlist", {
                    val on = !listed
                    listed = on
                    state.scope.launch {
                        try {
                            state.api?.setWatchlist(item.id, on)
                            state.refreshTick++
                        } catch (e: Exception) {
                            listed = !on
                            notices.show(e.message)
                        }
                    }
                }, Modifier.rememberFocus("act:watchlist"),
                tint = if (listed) Wren.ember else null,
            )
        }
        RoundButton(
            if (item.isWatched) Icons.Rounded.CheckCircle else Icons.Rounded.Visibility,
            if (item.isWatched) "Watched · Mark Unwatched" else "Mark as Watched", { toggleWatched(item) }, Modifier.rememberFocus("act:watched"),
            tint = if (item.isWatched) Wren.ember else null,
        )
        if (item.isPlayable && detail?.canDownload == true && (!detail.files.isNullOrEmpty() || detail.streaming == true)) {
            val x = state.downloads.item(item.id)
            when {
                x?.status == OfflineItem.Status.DONE -> RoundButton(Icons.Rounded.DownloadDone, "Downloaded", { open(DetailPanel.Download(item)) }, Modifier.rememberFocus("act:dl"), tint = Wren.ember)
                x != null && (x.active || x.status == OfflineItem.Status.WAITING) ->
                    PillButton(if (x.status == OfflineItem.Status.DOWNLOADING) "${(x.fraction * 100).toInt()}%" else x.message ?: "Preparing…",
                        { open(DetailPanel.Download(item)) }, Modifier.rememberFocus("act:dl"), icon = Icons.Rounded.Download)
                else -> RoundButton(Icons.Rounded.Download, "Download", { open(DetailPanel.Download(item)) }, Modifier.rememberFocus("act:dl"))
            }
        }
        if (state.isAdmin && (item.kind == "movie" || item.kind == "show")) {
            RoundButton(Icons.Rounded.Add, "Add to Collection", { open(DetailPanel.Collections) }, Modifier.rememberFocus("act:collections"))
        }
        if (state.isAdmin && detail?.streaming == true) {
            RoundButton(Icons.Rounded.CellTower, "Streaming Options", { open(DetailPanel.StreamAdmin) }, Modifier.rememberFocus("act:stream"))
        }
    }
}

/** Saving to this TV: start, pause, or play and remove a finished copy. */
@Composable
private fun DownloadPanel(item: Item, show: Item?, fileId: String?, close: () -> Unit) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val x = state.downloads.item(item.id)
    Panel(close, if (item.kind == "episode") "${item.episodeLabel} · ${item.title}" else item.title) {
        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            when {
                x?.status == OfflineItem.Status.DONE -> {
                    ListRow("Play Downloaded Copy", {
                        close()
                        state.downloads.localFile(x)?.let { f ->
                            nav.play(PlayTarget(x.id, x.displayTitle, x.subtitle, localFile = f.absolutePath, localPosition = x.position))
                        }
                    }, Modifier.rememberFocus("dl:play", default = true), detail = formatBytes(x.size))
                    ListRow("Remove Download", { close(); state.downloads.remove(x.id) })
                }
                x != null && (x.active || x.status == OfflineItem.Status.WAITING) -> {
                    ListRow("Pause Download", { close(); state.downloads.pause(x.id) }, Modifier.rememberFocus("dl:pause", default = true),
                        detail = if (x.size > 0) "${formatBytes(x.received)} of ${formatBytes(x.size)}" else x.message)
                    ListRow("Cancel Download", { close(); state.downloads.remove(x.id) })
                }
                else -> {
                    ListRow(
                        when (x?.status) { OfflineItem.Status.FAILED -> "Retry Download"; OfflineItem.Status.PAUSED -> "Resume Download"; else -> "Download to This TV" },
                        {
                            close()
                            if (x?.status == OfflineItem.Status.PAUSED) state.downloads.resume(x.id)
                            else state.downloads.add(item, show, fileId, state.server?.name ?: "")
                        },
                        Modifier.rememberFocus("dl:add", default = true),
                        detail = x?.message ?: "Watch without a connection to the server",
                    )
                    if (x != null) ListRow("Remove", { close(); state.downloads.remove(x.id) })
                }
            }
        }
    }
}

/** Adds the title to hand-picked collections, or takes it out (admins). */
@Composable
private fun CollectionsPanel(item: Item, close: () -> Unit) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var collections by remember { mutableStateOf<List<WrenCollection>?>(null) }
    var member by remember { mutableStateOf<Set<String>>(emptySet()) }
    LaunchedEffect(item.id) {
        val api = state.api ?: return@LaunchedEffect
        collections = runCatching { api.collections() }.getOrDefault(emptyList()).filter { it.kind == "manual" }
        member = runCatching { api.itemCollections(item.id) }.getOrDefault(emptyList()).toSet()
    }
    Panel(close, "Collections") {
        val list = collections
        when {
            list == null -> Loading(Modifier.padding(vertical = 20.dp))
            list.isEmpty() -> Text("No hand-picked collections yet. Create them in the web app under Collections.",
                style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
            else -> LazyColumn {
                items(list, key = { it.id }) { c ->
                    ToggleRow(c.name, c.id in member, { on ->
                        member = if (on) member + c.id else member - c.id
                        scope.launch {
                            runCatching {
                                if (on) state.api?.addToCollection(c.id, listOf(item.id)) else state.api?.removeFromCollection(c.id, item.id)
                            }
                            state.refreshTick++
                        }
                    }, Modifier.rememberFocus("col:${c.id}", default = c == list.first()))
                }
            }
        }
    }
}

// Seasons & episodes

@Composable
private fun Seasons(show: Item, detail: ItemDetail, play: (Item, Boolean) -> Unit, open: (DetailPanel) -> Unit) {
    val state = LocalAppState.current
    val seasons = detail.children.orEmpty()
    val default = remember(seasons, detail.nextEpisode) {
        seasons.firstOrNull { it.id == detail.nextEpisode?.parentId } ?: seasons.firstOrNull { it.seasonNumber > 0 } ?: seasons.first()
    }
    var seasonId by rememberSaveable(show.id) { mutableStateOf<String?>(null) }
    val season = seasons.firstOrNull { it.id == seasonId } ?: default
    val episodes = rememberLoaded<List<Item>>("season:${season.id}", state.refreshTick) { state.api?.item(season.id)?.children }
    Column {
        LazyRow(
            contentPadding = PaddingValues(horizontal = PageGutter, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            items(seasons, key = { it.id }) { s ->
                Chip(s.displayTitle, s.id == season.id, { seasonId = s.id }, Modifier.rememberFocus("season:${s.id}"))
            }
            val eps = episodes.value.orEmpty()
            if (detail.canDownload == true && eps.isNotEmpty()) {
                item("download") {
                    val missing = eps.filter { state.downloads.item(it.id) == null }
                    Spacer(Modifier.width(12.dp))
                    Chip(if (missing.isEmpty()) "Season Downloaded" else "Download Season", false, {
                        missing.forEach { state.downloads.add(it, show, server = state.server?.name ?: "") }
                    }, Modifier.rememberFocus("season:download"), icon = if (missing.isEmpty()) Icons.Rounded.DownloadDone else Icons.Rounded.Download)
                }
            }
        }
        val eps = episodes.value
        if (eps == null) Loading(Modifier.height(200.dp))
        else EpisodeRow(eps, play) { open(DetailPanel.Episode(it)) }
    }
}

@Composable
private fun EpisodeRow(episodes: List<Item>, play: (Item, Boolean) -> Unit, menu: (Item) -> Unit) {
    val state = LocalAppState.current
    // Start at the next episode to watch.
    val list = rememberLazyListState(initialFirstVisibleItemIndex = episodes.indexOfFirst { !it.isWatched }.coerceAtLeast(0))
    LazyRow(
        state = list,
        contentPadding = PaddingValues(horizontal = PageGutter, vertical = 16.dp),
        horizontalArrangement = Arrangement.spacedBy(20.dp),
    ) {
        items(episodes, key = { it.id }) { ep ->
            EpisodeCard(ep, state.downloads.item(ep.id), { play(ep, false) }, { menu(ep) }, Modifier.rememberFocus("ep:${ep.id}"))
        }
    }
}

@Composable
private fun EpisodeCard(ep: Item, offline: OfflineItem?, onClick: () -> Unit, onLongClick: () -> Unit, modifier: Modifier = Modifier) {
    val api = LocalAppState.current.api
    var focused by remember { mutableStateOf(false) }
    val width = 320.dp
    Column(modifier.width(width)) {
        ArtCard(onClick, Modifier.width(width).aspectRatio(16f / 9f).onFocusChanged { focused = it.hasFocus }, onLongClick) {
            RemoteImage(api?.imageUrl(ep.stillPath, "w780"), Modifier.fillMaxSize())
            ep.progress?.takeIf { it.position > 0 && !it.watched }?.let { ProgressStrip(it.fraction, Modifier.align(Alignment.BottomCenter)) }
            Row(Modifier.align(Alignment.TopEnd).padding(8.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                if (offline != null) {
                    Icon(if (offline.status == OfflineItem.Status.DONE) Icons.Rounded.DownloadDone else Icons.Rounded.Download, "Downloaded",
                        Modifier.size(26.dp).background(Color.Black.copy(alpha = 0.6f), CircleShape).padding(4.dp), tint = Color.White)
                }
                if (ep.isWatched) {
                    Icon(Icons.Rounded.CheckCircle, "Watched", Modifier.size(26.dp).background(Wren.ember, CircleShape), tint = Color.White)
                }
            }
        }
        Column(Modifier.padding(top = 10.dp).alpha(if (focused) 1f else 0.8f)) {
            Text("${ep.episodeNumber}. ${ep.title}", style = MaterialTheme.typography.titleSmall, color = Wren.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(listOfNotNull(ep.airDate?.takeIf { it.isNotEmpty() }, ep.runtimeMin?.takeIf { it > 0 }?.let { formatDuration(it * 60.0) }, ep.progress?.let(::timeLeft))
                .joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = Wren.secondary, maxLines = 1)
            if (focused) {
                ep.overview?.takeIf { it.isNotEmpty() }?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = Wren.secondary, maxLines = 3, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 4.dp))
                }
            }
        }
    }
}

// Cast & media

@Composable
private fun CastShelf(cast: List<CastMember>) {
    val api = LocalAppState.current.api
    Shelf("Cast") {
        items(cast.take(20), key = { it.name + it.character }) { c ->
            var focused by remember { mutableStateOf(false) }
            Column(Modifier.width(120.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                // Focusable so the row scrolls with the remote; there's nothing to open.
                ArtCard({}, Modifier.size(110.dp).onFocusChanged { focused = it.hasFocus }.rememberFocus("cast:${c.name}"), shape = RoundedCornerShape(55.dp)) {
                    RemoteImage(api?.imageUrl(c.profilePath, "w185"), Modifier.fillMaxSize().clip(CircleShape))
                    if (c.profilePath == null) {
                        Text(c.name.split(" ").mapNotNull { it.firstOrNull()?.toString() }.take(2).joinToString(""),
                            style = MaterialTheme.typography.headlineSmall, color = Wren.secondary, modifier = Modifier.align(Alignment.Center))
                    }
                }
                Spacer(Modifier.height(8.dp))
                Text(c.name, style = MaterialTheme.typography.labelLarge, color = if (focused) Wren.text else Wren.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
                c.character?.takeIf { it.isNotEmpty() }?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

@Composable
private fun FileSection(files: List<MediaFileView>) {
    val state = LocalAppState.current
    Column(Modifier.padding(horizontal = PageGutter).fillMaxWidth()) {
        SectionTitle("Media")
        files.first().info?.chapters?.size?.takeIf { it > 0 }?.let {
            Text("$it chapters", style = MaterialTheme.typography.bodySmall, color = Wren.secondary)
        }
        Spacer(Modifier.height(10.dp))
        files.forEach { f ->
            Column(Modifier.padding(vertical = 6.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Icon(Icons.Rounded.Movie, null, Modifier.size(18.dp), tint = Wren.tertiary)
                    Text(f.fileName.ifEmpty { f.label ?: "Stream" }, style = MaterialTheme.typography.bodyMedium, color = Wren.text, maxLines = 1, overflow = TextOverflow.MiddleEllipsis)
                }
                val parts = listOfNotNull(
                    fileSummary(f).ifEmpty { null },
                    if (f.streamId != null && state.isAdmin) f.release?.let { "Stream · $it" } ?: "Stream" else null,
                )
                if (parts.isNotEmpty()) Text(parts.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = Wren.secondary, maxLines = 1,
                    modifier = Modifier.padding(start = 26.dp))
                f.badges.takeIf { it.isNotEmpty() }?.let { b ->
                    Row(Modifier.padding(start = 26.dp, top = 4.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) { b.forEach { Badge(it) } }
                }
            }
        }
    }
}
