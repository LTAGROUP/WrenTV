package com.wrentv.tv.ui.screens

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.PhoneAndroid
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.R
import com.wrentv.tv.data.PairStart
import com.wrentv.tv.data.WrenTVException
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.QrCode
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.Spinner
import com.wrentv.tv.ui.TvTextField
import com.wrentv.tv.ui.Wren
import kotlinx.coroutines.android.awaitFrame
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

@Composable
fun ConnectScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val scope = rememberCoroutineScope()
    var address by remember { mutableStateOf(state.serverUrl?.toString()?.trimEnd('/') ?: "") }
    var connected by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    fun connect() {
        busy = true
        error = null
        scope.launch {
            try {
                state.connect(address)
                connected = true
            } catch (e: Exception) {
                error = "Couldn't reach the server: ${e.message}"
            } finally {
                busy = false
            }
        }
    }

    // Reconnect to the last server straight away.
    LaunchedEffect(Unit) { if (address.isNotEmpty()) connect() }

    Box(
        Modifier.fillMaxSize().background(
            Brush.radialGradient(listOf(Wren.ember.copy(alpha = 0.16f), Color.Transparent), radius = 1400f, center = androidx.compose.ui.geometry.Offset(0f, 0f)),
        ),
    ) {
        val server = state.server
        // The phone sign-in card sits beside the form, full height, on a 960x540 screen.
        val pairing = connected && server != null && state.accessIssue == null && !server.setupRequired
        Row(Modifier.fillMaxSize().padding(horizontal = 64.dp, vertical = 36.dp), horizontalArrangement = Arrangement.spacedBy(48.dp)) {
            Column(Modifier.weight(1f).fillMaxHeight().verticalScroll(rememberScrollState())) {
                Image(painterResource(R.drawable.lockup), "Wren TV", Modifier.height(36.dp))
                Spacer(Modifier.height(32.dp))
                if (!connected || server == null) {
                    Text("Connect to your server", style = MaterialTheme.typography.displaySmall, color = Wren.text)
                    Spacer(Modifier.height(8.dp))
                    Text("Enter the address of your Wren TV server, like 192.168.1.20 or wrentv.example.com.",
                        style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
                    Spacer(Modifier.height(28.dp))
                    val first = remember { FocusRequester() }
                    LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }
                    TvTextField(address, { address = it }, "Server address", Modifier.focusRequester(first),
                        placeholder = "192.168.1.20:32500", keyboard = KeyboardType.Uri, onDone = { if (address.isNotBlank()) connect() })
                    Spacer(Modifier.height(20.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
                        PillButton("Connect", { connect() }, primary = true, enabled = address.isNotBlank() && !busy)
                        if (state.downloads.items.isNotEmpty()) {
                            val n = state.downloads.items.count { it.status == com.wrentv.tv.data.OfflineItem.Status.DONE }
                            PillButton("Watch Downloads${if (n > 0) " ($n)" else ""}", { nav.push(Route.Downloads) }, icon = Icons.Rounded.Download)
                        }
                        if (busy) Spinner(32.dp)
                    }
                } else {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        Icon(Icons.Rounded.Dns, null, Modifier.size(24.dp), tint = Wren.secondary)
                        Text(server.name, style = MaterialTheme.typography.titleLarge, color = Wren.text, maxLines = 1,
                            overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                        PillButton("Change Server", { connected = false; state.accessIssue = null })
                    }
                    Spacer(Modifier.height(24.dp))
                    val issue = state.accessIssue
                    if (issue != null) {
                        AccessIssue(issue)
                    } else {
                        if (server.usesAccounts) AccountForm() else LocalForm(server.setupRequired)
                    }
                }
                error?.let {
                    Spacer(Modifier.height(16.dp))
                    Text(it, color = Wren.bad, style = MaterialTheme.typography.bodyLarge)
                }
            }
            // Approving from a phone needs someone already signed in.
            if (pairing) PairingCard(Modifier.align(Alignment.CenterVertically))
        }
    }
}

/** Sign in from a phone: scan the QR code (or open the link) and approve. */
@Composable
private fun PairingCard(modifier: Modifier = Modifier) {
    val state = LocalAppState.current
    var pair by remember { mutableStateOf<PairStart?>(null) }
    var unsupported by remember { mutableStateOf(false) }
    var attempt by remember { mutableStateOf(0) }
    LaunchedEffect(attempt, state.serverUrl) {
        pair = null
        val p = try {
            state.startPairing()
        } catch (e: WrenTVException) {
            // Servers from before TV sign-in codes don't have the endpoint.
            if (e.status == 404 || e.status == 405) unsupported = true
            return@LaunchedEffect
        } catch (e: Exception) {
            delay(5000)
            attempt++
            return@LaunchedEffect
        }
        pair = p
        val deadline = System.currentTimeMillis() + p.expiresIn * 1000L - 5000
        while (System.currentTimeMillis() < deadline) {
            delay(2000)
            try {
                if (state.pollPairing(p)) return@LaunchedEffect
            } catch (e: WrenTVException) {
                if (e.status == 404) break // expired
            } catch (_: Exception) {
                // Network blip: keep waiting.
            }
        }
        attempt++ // a fresh code
    }
    if (unsupported) return
    Column(
        modifier.width(340.dp).background(Wren.surface, RoundedCornerShape(24.dp)).padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Icon(Icons.Rounded.PhoneAndroid, null, Modifier.size(24.dp), tint = Wren.ember)
            Text("Sign in with your phone", style = MaterialTheme.typography.titleLarge, color = Wren.text)
        }
        Spacer(Modifier.height(16.dp))
        val p = pair
        if (p == null) {
            Box(Modifier.size(200.dp), contentAlignment = Alignment.Center) { Spinner() }
        } else {
            val link = state.pairingLink(p)
            QrCode(link, size = 200.dp)
            Spacer(Modifier.height(16.dp))
            Text(
                p.code.chunked(4).joinToString("-"),
                style = MaterialTheme.typography.headlineMedium.copy(fontFamily = FontFamily.Monospace, letterSpacing = 4.sp, fontWeight = FontWeight.Bold),
                color = Wren.text,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                "Scan with your phone's camera or the Wren TV app, or open ${link.substringBefore("?")} and enter the code.",
                style = MaterialTheme.typography.bodySmall, color = Wren.secondary,
                textAlign = androidx.compose.ui.text.style.TextAlign.Center,
            )
        }
    }
}

/** Fits beside the phone sign-in card. */
private val FieldWidth = 440.dp

@Composable
private fun AccountForm() {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var creating by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var email by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var notice by remember { mutableStateOf<String?>(null) }
    val first = remember { FocusRequester() }
    LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }

    fun submit() {
        busy = true
        error = null
        notice = null
        scope.launch {
            try {
                val ok = state.accountSignIn(email.trim(), password, if (creating) name.trim() else null)
                if (!ok) {
                    notice = "Check your email to confirm your account, then sign in."
                    creating = false
                }
                password = ""
            } catch (e: Exception) {
                error = e.message
            } finally {
                busy = false
            }
        }
    }

    Text(if (creating) "Create your Wren TV account" else "Sign in with your Wren TV account", style = MaterialTheme.typography.headlineSmall, color = Wren.text)
    Spacer(Modifier.height(16.dp))
    if (creating) {
        TvTextField(name, { name = it }, "Name", Modifier.focusRequester(first), width = FieldWidth)
        Spacer(Modifier.height(14.dp))
    }
    TvTextField(email, { email = it }, "Email", if (creating) Modifier else Modifier.focusRequester(first), keyboard = KeyboardType.Email, width = FieldWidth)
    Spacer(Modifier.height(10.dp))
    TvTextField(password, { password = it }, "Password", password = true, width = FieldWidth, onDone = { if (email.isNotBlank() && password.isNotEmpty()) submit() })
    Spacer(Modifier.height(18.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
        PillButton(if (creating) "Create Account" else "Sign In", { submit() }, primary = true,
            enabled = !busy && email.isNotBlank() && password.isNotEmpty() && (!creating || name.isNotBlank()))
        PillButton(if (creating) "I have an account" else "Create an account", { creating = !creating; error = null; notice = null })
        if (busy) Spinner(32.dp)
    }
    notice?.let { Spacer(Modifier.height(14.dp)); Text(it, color = Wren.secondary, style = MaterialTheme.typography.bodyLarge) }
    error?.let { Spacer(Modifier.height(14.dp)); Text(it, color = Wren.bad, style = MaterialTheme.typography.bodyLarge) }
}

@Composable
private fun LocalForm(setup: Boolean) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val first = remember { FocusRequester() }
    LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }

    fun submit() {
        busy = true
        error = null
        scope.launch {
            try {
                state.login(username.trim(), password, setup)
            } catch (e: Exception) {
                error = e.message
            } finally {
                busy = false
            }
        }
    }

    Text(if (setup) "Create the owner account for this server" else "Sign in", style = MaterialTheme.typography.headlineSmall, color = Wren.text)
    Spacer(Modifier.height(16.dp))
    TvTextField(username, { username = it }, "Username", Modifier.focusRequester(first), width = FieldWidth)
    Spacer(Modifier.height(10.dp))
    TvTextField(password, { password = it }, "Password", password = true, width = FieldWidth, onDone = { if (username.isNotBlank() && password.isNotEmpty()) submit() })
    Spacer(Modifier.height(18.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
        PillButton(if (setup) "Create Account" else "Sign In", { submit() }, primary = true, enabled = !busy && username.isNotBlank() && password.isNotEmpty())
        if (busy) Spinner(32.dp)
    }
    error?.let { Spacer(Modifier.height(14.dp)); Text(it, color = Wren.bad, style = MaterialTheme.typography.bodyLarge) }
}

@Composable
private fun AccessIssue(issue: String) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    Column(Modifier.width(720.dp)) {
        state.accountEmail?.let { Text(it, style = MaterialTheme.typography.titleMedium, color = Wren.secondary) }
        Spacer(Modifier.height(8.dp))
        Text(issue, style = MaterialTheme.typography.headlineSmall, color = Wren.text)
        Spacer(Modifier.height(24.dp))
        val first = remember { FocusRequester() }
        LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }
        Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
            PillButton("Check Again", {
                busy = true
                scope.launch { state.retryAccess(); busy = false }
            }, Modifier.focusRequester(first), primary = true)
            PillButton("Sign Out", { state.signOut() })
            if (busy) Spinner(32.dp)
        }
    }
}
