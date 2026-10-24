package com.wrentv.tv.ui.screens

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import com.wrentv.tv.ui.LocalRouteKey
import com.wrentv.tv.ui.LocalAmbient
import androidx.compose.ui.focus.onFocusChanged
import com.wrentv.tv.ui.LocalNavigator
import androidx.compose.ui.unit.sp
import androidx.compose.runtime.remember
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import kotlinx.coroutines.android.awaitFrame
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.wrentv.tv.data.Person
import com.wrentv.tv.ui.CinematicBrowse
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.TitleCard
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded

/**
 * An actor or crew member: photo, details and biography over a soft colour
 * from their photo, then rows of what they're in with the role under each
 * title. A focused title brings its artwork in; back up at the top, the page
 * is theirs again. Selecting a title opens its page; Back returns here.
 */
@Composable
fun PersonScreen(id: Int, name: String) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val route = LocalRouteKey.current
    val ambient = LocalAmbient.current
    val page = rememberLoaded<Person>("person:$id") { state.api?.person(id) }
    val list = rememberLazyListState()
    val p = page.value
    val facts = p?.facts.orEmpty()
    val hero = HeroEntry(
        id = "person:$id", title = p?.name ?: name, backdrop = null, logo = null,
        meta = facts, badges = emptyList(), overview = null, item = null,
    )
    // A biography too long for its card opens up into a page of its own to
    // read, over a plain background; a short one just grows in place.
    var reading by rememberSaveable(id) { mutableStateOf(false) }
    val chunks = remember(p?.biography) { p?.biography?.let(::bioChunks).orEmpty() }
    val knownFor = p?.rows?.firstOrNull()?.title?.equals("Known for", ignoreCase = true) == true
    // Swapping the card for the text (or back) drops focus onto the shelf
    // below for a moment, so it's moved across by hand once the new one is there.
    val bioStart = remember { FocusRequester() }
    val bioCard = remember { FocusRequester() }
    var refocus by remember { mutableStateOf<FocusRequester?>(null) }
    LaunchedEffect(refocus) {
        val target = refocus ?: return@LaunchedEffect
        repeat(10) {
            awaitFrame()
            if (runCatching { target.requestFocus() }.isSuccess) {
                refocus = null
                return@LaunchedEffect
            }
        }
        refocus = null
    }
    fun read(open: Boolean) {
        // Focus goes to the start of the text, or back to the card.
        nav.focus[route] = if (open) "person:$id:bio:0" else "person:$id:bio"
        reading = open
        refocus = if (open) bioStart else bioCard
    }
    // Not their best-known title's art: that reads as if its poster had focus.
    val atTop = { ambient.clear(route, hero, p?.profilePath) }
    LaunchedEffect(p?.profilePath) { if (ambient.route != route || ambient.path == null) atTop() }
    val readingRows = if (reading && chunks.isNotEmpty()) chunks.size + if (knownFor) 1 else 0 else 0
    // Back closes the text before it leaves the page.
    BackHandler(enabled = readingRows > 0) { read(false) }
    CinematicBrowse(list, fallback = hero, label = p?.knownFor?.takeIf { it.isNotEmpty() } ?: "Person", reading = readingRows) {
        when {
            p != null -> {
                if (readingRows > 0) {
                    row("about") { Box(Modifier.onFocusChanged { if (it.hasFocus) atTop() }) { ReadingHeader(p, chunks[0], bioStart) { read(false) } } }
                    chunks.drop(1).forEachIndexed { i, text ->
                        row("bio:${i + 1}") { BioChunk(p.id, i + 1, text, last = i + 2 == chunks.size) { read(false) } }
                    }
                } else {
                    row("about") { Box(Modifier.onFocusChanged { if (it.hasFocus) atTop() }) { About(p, bioCard) { read(true) } } }
                }
                p.rows.forEach { r ->
                    row("row:${r.title}") {
                        Shelf(r.title, subtitle = "${r.credits.size}") {
                            items(r.credits.distinctBy { it.id }, key = { it.id }) { t ->
                                TitleCard(t, Modifier.rememberFocus("person:$id:${r.title}:${t.id}"))
                            }
                        }
                    }
                }
                if (p.rows.isEmpty()) extra("empty") { EmptyState("No movies or shows listed", null, icon = Icons.Rounded.Movie) }
            }
            page.error != null -> extra("error") { EmptyState("Couldn't load", page.error, icon = Icons.Rounded.WifiOff) }
            else -> extra("loading") { Loading() }
        }
    }
}

/** The photo, or the person's initials when there isn't one. */
@Composable
private fun Photo(p: Person) {
    val api = LocalAppState.current.api
    Box(Modifier.size(130.dp, 195.dp).clip(RoundedCornerShape(12.dp)).background(Color.White.copy(alpha = 0.06f))) {
        RemoteImage(api?.imageUrl(p.profilePath, "w500"), Modifier.fillMaxSize())
        if (p.profilePath == null) {
            Text(p.name.split(" ").mapNotNull { it.firstOrNull()?.toString() }.take(2).joinToString(""),
                style = MaterialTheme.typography.headlineMedium, color = Wren.secondary, modifier = Modifier.align(Alignment.Center))
        }
    }
}

/**
 * The photo, and the biography as a card that opens up when selected. One
 * that runs past its five lines opens the reading page instead ([read]).
 */
@Composable
private fun About(p: Person, requester: FocusRequester, read: () -> Unit) {
    var open by rememberSaveable(p.id) { mutableStateOf(false) }
    var long by remember(p.id) { mutableStateOf(false) }
    Row(Modifier.padding(horizontal = PageGutter, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(28.dp)) {
        Photo(p)
        val bio = p.biography?.takeIf { it.isNotBlank() }
        if (bio != null) {
            Surface(
                onClick = { if (long) read() else open = !open },
                modifier = Modifier.widthIn(max = 720.dp).focusRequester(requester).rememberFocus("person:${p.id}:bio", default = true),
                shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(12.dp)),
                colors = ClickableSurfaceDefaults.colors(
                    containerColor = Color.White.copy(alpha = 0.06f), contentColor = Wren.text,
                    focusedContainerColor = Color.White.copy(alpha = 0.16f), focusedContentColor = Wren.text,
                ),
                scale = ClickableSurfaceDefaults.scale(focusedScale = 1.02f),
            ) {
                Column(Modifier.padding(18.dp)) {
                    Text("Biography", style = MaterialTheme.typography.titleSmall)
                    Text(bio, style = MaterialTheme.typography.bodyMedium, maxLines = if (open) 30 else 5, overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(top = 6.dp), onTextLayout = { if (!open && it.hasVisualOverflow) long = true })
                    Text(if (open) "Select to show less" else "Select to read more", style = MaterialTheme.typography.labelSmall,
                        color = Wren.secondary, modifier = Modifier.padding(top = 8.dp))
                }
            }
        }
    }
}

/** Where the reading text starts: photo, name and facts, then the first part of the biography. */
@Composable
private fun ReadingHeader(p: Person, first: String, requester: FocusRequester, close: () -> Unit) {
    Row(Modifier.padding(horizontal = PageGutter, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(28.dp)) {
        Photo(p)
        Column(Modifier.widthIn(max = ReadingWidth)) {
            Text(p.name, style = MaterialTheme.typography.headlineLarge, color = Wren.text)
            if (p.facts.isNotEmpty()) Box(Modifier.padding(top = 6.dp)) { MetaLine(p.facts) }
            Text("Biography", style = MaterialTheme.typography.titleSmall, color = Wren.secondary, modifier = Modifier.padding(top = 18.dp, start = 14.dp))
            BioText(p.id, 0, first, last = false, close, Modifier.focusRequester(requester))
        }
    }
}

/** A later part of the biography, lined up under the first. */
@Composable
private fun BioChunk(id: Int, index: Int, text: String, last: Boolean, close: () -> Unit) {
    Box(Modifier.padding(start = PageGutter + 130.dp + 28.dp, end = PageGutter).widthIn(max = ReadingWidth)) {
        BioText(id, index, text, last, close)
    }
}

/**
 * One part of the biography. Each takes focus in turn, so Down reads on
 * and the page scrolls with it; selecting any part closes the text.
 */
@Composable
private fun BioText(id: Int, index: Int, text: String, last: Boolean, close: () -> Unit, modifier: Modifier = Modifier) {
    Surface(
        onClick = close,
        modifier = modifier.rememberFocus("person:$id:bio:$index"),
        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(10.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Color.Transparent, contentColor = Wren.text.copy(alpha = 0.78f),
            focusedContainerColor = Color.White.copy(alpha = 0.05f), focusedContentColor = Wren.text,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1f),
    ) {
        Column(Modifier.padding(horizontal = 14.dp, vertical = 10.dp)) {
            Text(text, style = MaterialTheme.typography.bodyLarge.copy(lineHeight = 28.sp))
            if (last) {
                Text("Select to show less", style = MaterialTheme.typography.labelSmall, color = Wren.secondary, modifier = Modifier.padding(top = 10.dp))
            }
        }
    }
}

private val ReadingWidth = 760.dp

/**
 * The biography in parts of a few sentences, split at paragraphs and, for
 * a long paragraph, between sentences, so each part fits on screen.
 */
private fun bioChunks(bio: String, max: Int = 480): List<String> {
    val out = mutableListOf<String>()
    for (para in bio.split(Regex("\\n\\s*\\n|\\n")).map { it.trim() }.filter { it.isNotEmpty() }) {
        if (para.length <= max) { out += para; continue }
        var part = StringBuilder()
        for (sentence in para.split(Regex("(?<=[.!?])\\s+"))) {
            if (part.isNotEmpty() && part.length + sentence.length > max) {
                out += part.toString()
                part = StringBuilder()
            }
            if (part.isNotEmpty()) part.append(' ')
            part.append(sentence)
        }
        if (part.isNotEmpty()) out += part.toString()
    }
    return out
}
