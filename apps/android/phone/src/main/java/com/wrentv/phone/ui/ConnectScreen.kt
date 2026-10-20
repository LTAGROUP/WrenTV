package com.wrentv.phone.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.ArrowCircleDown
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.wrentv.phone.R
import com.wrentv.tv.data.AccountServer
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * Signing in, like the iPhone app: a Wren TV account first, which finds its
 * servers on its own; a server address for servers with their own usernames.
 */
@Composable
fun ConnectScreen() {
    val state = LocalAppState.current
    var byAddress by remember { mutableStateOf(false) }
    // Signing in to an account server with a local username instead.
    var useUsername by remember { mutableStateOf(false) }
    var offline by remember { mutableStateOf(false) }

    if (offline) {
        BackHandler { offline = false }
        Box(Modifier.fillMaxSize().systemBarsPadding()) {
            Column {
                TextButton({ offline = false }, Modifier.padding(8.dp)) { Text("Done") }
                Box(Modifier.weight(1f)) { DownloadsScreen() }
            }
        }
        return
    }

    Column(
        Modifier.fillMaxSize().systemBarsPadding().imePadding().verticalScroll(rememberScrollState()).padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(18.dp),
    ) {
        Image(painterResource(R.drawable.lockup), "Wren TV", Modifier.padding(top = 40.dp, bottom = 12.dp).widthIn(max = 220.dp))
        Column(Modifier.widthIn(max = 460.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            val server = state.server
            when {
                state.accessIssue != null -> AccessIssue(state.accessIssue!!)
                server != null && state.serverUrl != null -> {
                    BackHandler { state.switchServer() }
                    Text(server.name.ifEmpty { "Wren TV server" }, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
                    if (server.usesAccounts && !useUsername) {
                        AccountForm(server = true)
                        if (server.localAccounts == true) {
                            TextButton({ useUsername = true }, Modifier.fillMaxWidth()) { Text("Have a username for this server? Sign in with it") }
                        }
                    } else {
                        if (server.usesAccounts) Text("Sign in with the username the server's admin gave you.", color = Wren.secondary)
                        LocalLogin(server.needsOwner)
                        if (server.usesAccounts) {
                            TextButton({ useUsername = false }, Modifier.fillMaxWidth()) { Text("Sign in with a Wren TV account instead") }
                        }
                    }
                    TextButton({ state.switchServer() }, Modifier.fillMaxWidth()) { Text("Use a different server") }
                }
                state.account != null && !byAddress -> ServerPicker { byAddress = true }
                byAddress -> {
                    BackHandler { byAddress = false }
                    AddressForm()
                    TextButton({ byAddress = false }, Modifier.fillMaxWidth()) { Text(if (state.account != null) "Back to your servers" else "Sign in with a Wren TV account") }
                }
                else -> {
                    Text("Sign in", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
                    Text("Use your Wren TV account to find your servers and the ones shared with you.", color = Wren.secondary)
                    AccountForm(server = false)
                    TextButton({ byAddress = true }, Modifier.fillMaxWidth()) { Text("Connect to a server by address") }
                }
            }
            if (state.downloads.items.isNotEmpty()) {
                TextButton({ offline = true }) {
                    Icon(Icons.Rounded.ArrowCircleDown, null)
                    Text("  Open Downloads")
                }
            }
        }
    }
}

@Composable
private fun AccessIssue(message: String) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    Text("No access yet", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
    Text(message, color = Wren.secondary)
    Button({ busy = true; scope.launch { runCatching { state.retryAccess() }; busy = false } }, Modifier.fillMaxWidth(), enabled = !busy) {
        Text(if (busy) "Checking…" else "Check Again")
    }
    TextButton({ state.signOut() }, Modifier.fillMaxWidth()) { Text("Sign Out") }
}

@Composable
private fun AccountForm(server: Boolean) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var create by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var email by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var confirm by remember { mutableStateOf(false) }

    if (confirm) {
        Text("Check your email", fontWeight = FontWeight.Bold)
        Text("Open the link we sent to $email to confirm your account, then sign in.", color = Wren.secondary)
        Button({ confirm = false; create = false }, Modifier.fillMaxWidth()) { Text("Sign In") }
        return
    }
    if (create) Field("Name", name, { name = it })
    Field("Email", email, { email = it.trim() }, KeyboardType.Email)
    Field("Password", password, { password = it }, KeyboardType.Password, secret = true)
    error?.let { Text(it, color = Wren.ember) }
    Button(
        {
            busy = true; error = null
            scope.launch {
                try {
                    val n = if (create) name.ifBlank { email.substringBefore('@') } else null
                    val ok = if (server) state.accountSignIn(email, password, n) else state.signInToAccount(email, password, n)
                    if (!ok) confirm = true
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    error = e.message ?: "Sign-in failed"
                }
                busy = false
            }
        },
        Modifier.fillMaxWidth(), enabled = !busy && email.contains('@') && password.length >= 6,
    ) { Text(if (busy) "Signing in…" else if (create) "Create Account" else "Sign In") }
    TextButton({ create = !create; error = null }, Modifier.fillMaxWidth()) {
        Text(if (create) "I have an account" else "Create a Wren TV account")
    }
}

@Composable
private fun LocalLogin(setup: Boolean) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    if (setup) Text("This server is new. Create the owner account.", color = Wren.secondary)
    Field("Username", username, { username = it.trim() })
    Field("Password", password, { password = it }, KeyboardType.Password, secret = true)
    error?.let { Text(it, color = Wren.ember) }
    Button(
        {
            busy = true; error = null
            scope.launch {
                try { state.login(username, password, setup) } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message ?: "Sign-in failed" }
                busy = false
            }
        },
        Modifier.fillMaxWidth(), enabled = !busy && username.isNotEmpty() && password.isNotEmpty(),
    ) { Text(if (busy) "Signing in…" else if (setup) "Create Owner" else "Sign In") }
}

@Composable
private fun AddressForm() {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var address by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    Text("Connect by address", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
    Text("The server's address on your network, like 192.168.1.20 or a web address.", color = Wren.secondary)
    Field("Server address", address, { address = it.trim() }, KeyboardType.Uri)
    error?.let { Text(it, color = Wren.ember) }
    Button(
        {
            busy = true; error = null
            scope.launch {
                try { state.connect(address) } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message ?: "Couldn't connect" }
                busy = false
            }
        },
        Modifier.fillMaxWidth(), enabled = !busy && address.isNotEmpty(),
    ) { Text(if (busy) "Connecting…" else "Connect") }
}

@Composable
private fun ServerPicker(useAddress: () -> Unit) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var opening by remember { mutableStateOf<String?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    val servers = state.accountServers
    val me = state.account?.userId

    Row(verticalAlignment = Alignment.CenterVertically) {
        Text("Your servers", Modifier.weight(1f), style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
        androidx.compose.material3.IconButton({ scope.launch { state.loadAccountServers() } }) { Icon(Icons.Rounded.Refresh, "Refresh") }
    }
    state.accountEmail?.let { Text("Signed in as $it", color = Wren.secondary) }
    (error ?: state.accountServersError)?.let { Text(it, color = Wren.ember) }
    when {
        servers == null -> Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator(color = Color.White) }
        servers.isEmpty() -> Text(
            "No servers yet. Install Wren TV on a computer at home and sign in there with this account, or ask a friend to share theirs with this email address.",
            color = Wren.secondary,
        )
        else -> {
            val (mine, shared) = servers.partition { it.owner_id == me }
            if (mine.isNotEmpty()) ServerGroup("Yours", mine, opening) { s -> opening = s.id; error = null; scope.launch { open(state, s) { error = it }; opening = null } }
            if (shared.isNotEmpty()) ServerGroup("Shared with you", shared, opening) { s -> opening = s.id; error = null; scope.launch { open(state, s) { error = it }; opening = null } }
        }
    }
    TextButton(useAddress, Modifier.fillMaxWidth()) { Text("Use an address") }
    TextButton({ state.signOut() }, Modifier.fillMaxWidth()) { Text("Sign Out") }
}

private suspend fun open(state: com.wrentv.tv.data.AppState, s: AccountServer, fail: (String) -> Unit) {
    try {
        state.open(s)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        fail(e.message ?: "Couldn't connect")
    }
}

@Composable
private fun ServerGroup(title: String, servers: List<AccountServer>, opening: String?, pick: (AccountServer) -> Unit) {
    Text(title.uppercase(), color = Wren.secondary, style = MaterialTheme.typography.labelMedium)
    Column(Modifier.clip(RoundedCornerShape(12.dp)).background(Wren.surface)) {
        servers.forEach { s ->
            Row(
                Modifier.fillMaxWidth().clickable(enabled = opening == null) { pick(s) }.padding(horizontal = 16.dp, vertical = 14.dp),
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Icon(Icons.Rounded.Dns, null, tint = Wren.secondary)
                Column(Modifier.weight(1f)) {
                    Text(s.name, fontWeight = FontWeight.SemiBold)
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        Box(Modifier.size(7.dp).clip(CircleShape).background(if (s.online) Color(0xFF3FBF5F) else Wren.secondary))
                        Text(if (s.online) "Online" else "Offline", color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
                    }
                }
                if (opening == s.id) CircularProgressIndicator(Modifier.size(20.dp), color = Color.White, strokeWidth = 2.dp)
                else Icon(Icons.Rounded.ChevronRight, null, tint = Wren.secondary)
            }
        }
    }
}

@Composable
fun Field(label: String, value: String, onChange: (String) -> Unit, type: KeyboardType = KeyboardType.Text, secret: Boolean = false) {
    OutlinedTextField(
        value, onChange, Modifier.fillMaxWidth(), label = { Text(label) }, singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = type, autoCorrectEnabled = false),
        visualTransformation = if (secret) PasswordVisualTransformation() else androidx.compose.ui.text.input.VisualTransformation.None,
        colors = androidx.compose.material3.OutlinedTextFieldDefaults.colors(focusedBorderColor = Wren.ember, focusedLabelColor = Color.White, cursorColor = Color.White),
    )
}

