package com.wrentv.phone.ui

import android.content.Intent
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.net.toUri
import com.wrentv.tv.data.Person
import kotlinx.coroutines.CancellationException

/**
 * An actor or crew member over their best-known title's artwork: photo,
 * details and biography, then rows of what they're in with the role under
 * each title. Tapping a title opens its page.
 */
@Composable
fun PersonScreen(id: Int, name: String) {
    val state = LocalAppState.current
    val m = metrics()
    val context = LocalContext.current
    var person by remember(id) { mutableStateOf<Person?>(null) }
    var error by remember(id) { mutableStateOf<String?>(null) }
    var bioOpen by rememberSaveable(id) { mutableStateOf(false) }
    LaunchedEffect(id, state.api) {
        try { person = state.api?.person(id); error = null } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message }
    }
    val p = person
    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(28.dp), contentPadding = PaddingValues(bottom = 16.dp)) {
            item("header") {
                Column {
                    Box(Modifier.fillMaxWidth().height(if (m.compact) 320.dp else 400.dp)) {
                        RemoteImage(state.api?.imageUrl(p?.backdropPath, "w1280"), Modifier.fillMaxSize())
                        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.2f to Color.Transparent, 1f to Wren.background)))
                        Row(Modifier.align(Alignment.BottomStart).padding(horizontal = m.gutter), horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.Bottom) {
                            Box(Modifier.size(104.dp, 156.dp).clip(RoundedCornerShape(10.dp)).background(Color.White.copy(alpha = 0.06f)), contentAlignment = Alignment.Center) {
                                RemoteImage(state.api?.imageUrl(p?.profilePath, "w500"), Modifier.fillMaxSize())
                                if (p != null && p.profilePath == null) {
                                    Text(name.split(" ").take(2).mapNotNull { it.firstOrNull() }.joinToString(""), fontSize = 28.sp, fontWeight = FontWeight.Bold, color = Wren.secondary)
                                }
                            }
                            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                p?.knownFor?.takeIf { it.isNotEmpty() }?.let { Text(it.uppercase(), fontSize = 12.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.sp, color = Wren.ember) }
                                Text(p?.name ?: name, fontSize = 28.sp, fontWeight = FontWeight.Bold, lineHeight = 32.sp)
                                p?.facts?.forEach { Text(it, color = Wren.secondary, style = MaterialTheme.typography.bodySmall) }
                            }
                        }
                    }
                    p?.biography?.takeIf { it.isNotBlank() }?.let { bio ->
                        Column(Modifier.padding(horizontal = m.gutter).padding(top = 16.dp)) {
                            Text(bio, Modifier.clickable { bioOpen = !bioOpen }, maxLines = if (bioOpen) Int.MAX_VALUE else 5, overflow = TextOverflow.Ellipsis,
                                style = MaterialTheme.typography.bodyMedium, color = Wren.text)
                            Row {
                                TextButton({ bioOpen = !bioOpen }) { Text(if (bioOpen) "Less" else "More", color = Wren.ember) }
                                p.imdbId?.let { imdb ->
                                    TextButton({ context.startActivity(Intent(Intent.ACTION_VIEW, "https://www.imdb.com/name/$imdb/".toUri())) }) { Text("IMDb", color = Wren.secondary) }
                                }
                                TextButton({ context.startActivity(Intent(Intent.ACTION_VIEW, "https://www.themoviedb.org/person/$id".toUri())) }) { Text("TMDB", color = Wren.secondary) }
                            }
                        }
                    }
                }
            }
            when {
                p != null -> {
                    items(p.rows, { it.title }) { r ->
                        Shelf(r.title, "${r.credits.size}") {
                            items(r.credits.distinctBy { it.id }, { it.id }) { TitleCard(it, width = m.poster) }
                        }
                    }
                    if (p.rows.isEmpty()) item { EmptyState(Icons.Rounded.Movie, "No movies or shows listed", null, Modifier.fillMaxWidth().padding(top = 40.dp)) }
                }
                error != null -> item { EmptyState(Icons.Rounded.Warning, "Couldn't load", error, Modifier.fillMaxWidth().padding(top = 40.dp)) }
                else -> item { Loading(Modifier.fillMaxWidth().padding(top = 60.dp)) }
            }
        }
        FloatingBack()
    }
}
