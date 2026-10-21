package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
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
 * An actor or crew member over their best-known title's artwork: photo,
 * details and biography, then rows of what they're in with the role under
 * each title. Selecting a title opens its page; Back returns here.
 */
@Composable
fun PersonScreen(id: Int, name: String) {
    val state = LocalAppState.current
    val page = rememberLoaded<Person>("person:$id") { state.api?.person(id) }
    val list = rememberLazyListState()
    val p = page.value
    val facts = p?.facts.orEmpty()
    val hero = HeroEntry(
        id = "person:$id", title = p?.name ?: name, backdrop = p?.backdropPath, logo = null,
        meta = facts, badges = emptyList(), overview = null, item = null,
    )
    CinematicBrowse(list, fallback = hero, label = p?.knownFor?.takeIf { it.isNotEmpty() } ?: "Person") {
        when {
            p != null -> {
                row("about") { About(p) }
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

/** The photo, and the biography as a card that opens up when selected. */
@Composable
private fun About(p: Person) {
    val api = LocalAppState.current.api
    var open by rememberSaveable(p.id) { mutableStateOf(false) }
    Row(Modifier.padding(horizontal = PageGutter, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(28.dp)) {
        Box(Modifier.size(130.dp, 195.dp).clip(RoundedCornerShape(12.dp)).background(Color.White.copy(alpha = 0.06f))) {
            RemoteImage(api?.imageUrl(p.profilePath, "w500"), Modifier.fillMaxSize())
            if (p.profilePath == null) {
                Text(p.name.split(" ").mapNotNull { it.firstOrNull()?.toString() }.take(2).joinToString(""),
                    style = MaterialTheme.typography.headlineMedium, color = Wren.secondary, modifier = Modifier.align(Alignment.Center))
            }
        }
        val bio = p.biography?.takeIf { it.isNotBlank() }
        if (bio != null) {
            Surface(
                onClick = { open = !open },
                modifier = Modifier.widthIn(max = 720.dp).rememberFocus("person:${p.id}:bio", default = true),
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
                        modifier = Modifier.padding(top = 6.dp))
                    Text(if (open) "Select to show less" else "Select to read more", style = MaterialTheme.typography.labelSmall,
                        color = Wren.secondary, modifier = Modifier.padding(top = 8.dp))
                }
            }
        }
    }
}
