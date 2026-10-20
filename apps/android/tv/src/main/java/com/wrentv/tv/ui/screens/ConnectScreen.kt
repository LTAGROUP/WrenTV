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
import com.wrentv.tv.data.DeviceLink
import com.wrentv.tv.data.PairStart
import com.wrentv.tv.data.WrenTVException
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.ListRow
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
    // Typing an address is the fallback; a remembered one is from a server without accounts.
    var byAddress by remember { mutableStateOf(state.account == null && state.serverUrl != null) }
    Box(
        Modifier.fillMaxSize().background(
            Brush.radialGradient(listOf(Wren.ember.copy(alpha = 0.16f), Color.Transparent), radius = 1400f, center = androidx.compose.ui.geometry.Offset(0f, 0f)),
        ),
    ) {
        val server = state.server
        when {
            state.account != null && server != null && state.serverUrl != null -> Page { AccountServer(server) }
            state.account != null -> Page { ServerPicker { byAddress = true } }
            byAddress -> ByAddress { byAddress = false }
            else -> Page(side = { AccountLinkCard(it) }) { AccountStart { byAddress = true } }
        }
    }
}

/** The logo, a scrolling column, and an optional card on the right (full height, on a 960x540 screen). */
@Composable
private fun Page(side: (@Composable (Modifier) -> Unit)? = null, content: @Composable () -> Unit) {
    Row(Modifier.fillMaxSize().padding(horizontal = 64.dp, vertical = 36.dp), horizontalArrangement = Arrangement.spacedBy(48.dp)) {
        Column(Modifier.weight(1f).fillMaxHeight().verticalScroll(rememberScrollState())) {
            Image(painterResource(R.drawable.lockup), "Wren TV", Modifier.height(36.dp))
            Spacer(Modifier.height(32.dp))
            content()
        }
        side?.invoke(Modifier.align(Alignment.CenterVertically))
    }
}

@Composable
private fun WatchDownloads() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    if (state.downloads.items.isEmpty()) return
    val n = state.downloads.items.count { it.status == com.wrentv.tv.data.OfflineItem.Status.DONE }
    PillButton("Watch Downloads${if (n > 0) " ($n)" else ""}", { nav.push(Route.Downloads) }, icon = Icons.Rounded.Download)
}

/** Signed out: the account's email and password here, or the QR code beside it. */
@Composable
private fun AccountStart(useAddress: () -> Unit) {
    val state = LocalAppState.current
    AccountForm(title = "Sign in to Wren TV") { email, password, name -> state.signInToAccount(email, password, name) }
    Spacer(Modifier.height(22.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
        PillButton("Connect by Address", useAddress, icon = Icons.Rounded.Dns)
        WatchDownloads()
    }
}

/** Sign in from a phone without knowing any server: scan the QR code and approve with the account. */
@Composable
private fun AccountLinkCard(modifier: Modifier = Modifier) {
    val state = LocalAppState.current
    var link by remember { mutableStateOf<DeviceLink?>(null) }
    var attempt by remember { mutableStateOf(0) }
    LaunchedEffect(attempt) {
        link = null
        val l = try {
            state.startAccountLink()
        } catch (e: Exception) {
            delay(5000)
            attempt++
            return@LaunchedEffect
        }
        link = l
        val deadline = System.currentTimeMillis() + 10 * 60_000L - 5000
        while (System.currentTimeMillis() < deadline) {
            delay(2000)
            try {
                if (state.pollAccountLink(l)) return@LaunchedEffect
            } catch (e: WrenTVException) {
                if (e.status == 404) break // expired
            } catch (_: Exception) {
                // Network blip: keep waiting.
            }
        }
        attempt++ // a fresh code
    }
    val l = link
    LinkCard(l?.code, l?.let { "$LinkPage?code=${it.code}" },
        "Scan with your phone's camera or the Wren TV app, or go to ${LinkPage.removePrefix("https://")} and enter the code.", modifier)
}

/** Where a phone approves a TV's account sign-in code. */
private const val LinkPage = "https://watch.wrentv.com/link"

@Composable
private fun LinkCard(code: String?, link: String?, hint: String, modifier: Modifier) {
    Column(
        modifier.width(340.dp).background(Wren.surface, RoundedCornerShape(24.dp)).padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Icon(Icons.Rounded.PhoneAndroid, null, Modifier.size(24.dp), tint = Wren.ember)
            Text("Sign in with your phone", style = MaterialTheme.typography.titleLarge, color = Wren.text)
        }
        Spacer(Modifier.height(16.dp))
        if (code == null || link == null) {
            Box(Modifier.size(200.dp), contentAlignment = Alignment.Center) { Spinner() }
        } else {
            QrCode(link, size = 200.dp)
            Spacer(Modifier.height(16.dp))
            Text(
                code.chunked(4).joinToString("-"),
                style = MaterialTheme.typography.headlineMedium.copy(fontFamily = FontFamily.Monospace, letterSpacing = 4.sp, fontWeight = FontWeight.Bold),
                color = Wren.text,
            )
            Spacer(Modifier.height(8.dp))
            Text(hint, style = MaterialTheme.typography.bodySmall, color = Wren.secondary, textAlign = androidx.compose.ui.text.style.TextAlign.Center)
        }
    }
}

/** Signed in to the account: its servers. One server (or one online) opens by itself. */
@Composable
private fun ServerPicker(useAddress: () -> Unit) {
    val state = LocalAppState.current
    var opening by remember { mutableStateOf<String?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    val first = remember { FocusRequester() }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(state.accountEmail ?: "", style = MaterialTheme.typography.titleMedium, color = Wren.secondary, maxLines = 1,
            overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
        PillButton("Sign Out", { state.signOut() })
    }
    Spacer(Modifier.height(20.dp))
    Text("Your servers", style = MaterialTheme.typography.headlineSmall, color = Wren.text)
    Spacer(Modifier.height(12.dp))
    val servers = state.accountServers
    when {
        servers == null && state.accountServersError == null -> Spinner(32.dp)
        servers.isNullOrEmpty() -> Text(
            state.accountServersError ?: "No servers yet. Install Wren TV on a computer and claim it with this account, or ask a server's owner to share it with ${state.accountEmail ?: "this email"}.",
            style = MaterialTheme.typography.bodyLarge, color = Wren.secondary, modifier = Modifier.width(720.dp),
        )
        else -> Column(Modifier.width(720.dp)) {
            LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }
            servers.forEachIndexed { i, s ->
                val whose = if (s.owner_id == state.account?.userId) "Yours" else "Shared with you"
                ListRow(s.name, {
                    if (opening != null) return@ListRow
                    opening = s.id
                    error = null
                    state.scope.launch {
                        try {
                            state.open(s)
                        } catch (e: Exception) {
                            error = e.message
                        } finally {
                            opening = null
                        }
                    }
                }, if (i == 0) Modifier.focusRequester(first) else Modifier,
                    detail = whose + if (s.online) " · online" else " · offline",
                    value = if (opening == s.id) "Connecting…" else null,
                    leading = { Icon(Icons.Rounded.Dns, null, Modifier.size(24.dp), tint = Wren.ember) })
            }
            if (state.accountServersError != null) Text(state.accountServersError!!, color = Wren.bad, style = MaterialTheme.typography.bodyLarge)
        }
    }
    error?.let { Spacer(Modifier.height(14.dp)); Text(it, color = Wren.bad, style = MaterialTheme.typography.bodyLarge) }
    Spacer(Modifier.height(22.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
        PillButton("Refresh", { state.scope.launch { state.loadAccountServers() } }, if (servers.isNullOrEmpty()) Modifier.focusRequester(first) else Modifier)
        PillButton("Connect by Address", useAddress)
        WatchDownloads()
    }
    if (servers.isNullOrEmpty()) LaunchedEffect(servers) { awaitFrame(); runCatching { first.requestFocus() } }
}

/** Opened one of the account's servers: connecting, or why it can't be used yet. */
@Composable
private fun AccountServer(server: com.wrentv.tv.data.ServerInfo) {
    val state = LocalAppState.current
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Icon(Icons.Rounded.Dns, null, Modifier.size(24.dp), tint = Wren.secondary)
        Text(server.name, style = MaterialTheme.typography.titleLarge, color = Wren.text, maxLines = 1,
            overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
    }
    Spacer(Modifier.height(24.dp))
    val issue = state.accessIssue
    if (issue != null) AccessIssue(issue, change = { state.switchServer() }) else Spinner(32.dp)
}

/** Connecting by typing a server's address (servers with their own usernames, or without internet). */
@Composable
private fun ByAddress(back: () -> Unit) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var address by remember { mutableStateOf(state.serverUrl?.toString()?.trimEnd('/') ?: "") }
    var connected by remember { mutableStateOf(false) }
    // Signing in to an account server with a local username instead.
    var useUsername by remember { mutableStateOf(false) }
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

    val server = state.server
    // Approving from a phone needs someone already signed in.
    val pairing = connected && server != null && state.accessIssue == null && !server.needsOwner &&
        (!server.usesAccounts || server.localAccounts == true)
    Page(side = if (pairing) { m -> PairingCard(m) } else null) {
        if (!connected || server == null) {
            Text("Connect to a server", style = MaterialTheme.typography.displaySmall, color = Wren.text)
            Spacer(Modifier.height(8.dp))
            Text("Enter the address of a Wren TV server, like 192.168.1.20 or wrentv.example.com.",
                style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
            Spacer(Modifier.height(28.dp))
            val first = remember { FocusRequester() }
            LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }
            TvTextField(address, { address = it }, "Server address", Modifier.focusRequester(first),
                placeholder = "192.168.1.20:32500", keyboard = KeyboardType.Uri, onDone = { if (address.isNotBlank()) connect() })
            Spacer(Modifier.height(20.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
                PillButton("Connect", { connect() }, primary = true, enabled = address.isNotBlank() && !busy)
                PillButton("Sign in with an Account", back)
                WatchDownloads()
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
            } else if (server.usesAccounts && !useUsername) {
                AccountForm(title = "Sign in with your Wren TV account") { email, password, name -> state.accountSignIn(email, password, name) }
                if (server.localAccounts == true) {
                    Spacer(Modifier.height(16.dp))
                    PillButton("Sign in with a Username Instead", { useUsername = true })
                }
            } else {
                LocalForm(server.needsOwner)
                if (server.usesAccounts) {
                    Spacer(Modifier.height(16.dp))
                    PillButton("Sign in with a Wren TV Account Instead", { useUsername = false })
                }
            }
        }
        error?.let {
            Spacer(Modifier.height(16.dp))
            Text(it, color = Wren.bad, style = MaterialTheme.typography.bodyLarge)
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
    val p = pair
    val link = p?.let { state.pairingLink(it) }
    LinkCard(p?.code, link, "Scan with your phone's camera or the Wren TV app, or open ${link?.substringBefore("?") ?: "the link"} and enter the code.", modifier)
}

/** Fits beside the phone sign-in card. */
private val FieldWidth = 440.dp

@Composable
private fun AccountForm(title: String, signIn: suspend (email: String, password: String, name: String?) -> Boolean) {
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
                val ok = signIn(email.trim(), password, if (creating) name.trim() else null)
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

    Text(if (creating) "Create your Wren TV account" else title, style = MaterialTheme.typography.headlineSmall, color = Wren.text)
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
private fun AccessIssue(issue: String, change: (() -> Unit)? = null) {
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
            change?.let { PillButton("Other Servers", it) }
            PillButton("Sign Out", { state.signOut() })
            if (busy) Spinner(32.dp)
        }
    }
}
