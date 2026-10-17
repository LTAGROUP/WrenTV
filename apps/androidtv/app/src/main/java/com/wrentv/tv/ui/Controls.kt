package com.wrentv.tv.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Check
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.android.awaitFrame

// Loading

/** Loaded page data, kept in memory so going back shows it at once. */
object PageCache {
    val values = HashMap<String, Any?>()
}

@Stable
class Loaded<T>(initial: T?) {
    var value by mutableStateOf(initial)
    var error by mutableStateOf<String?>(null)
    var loading by mutableStateOf(initial == null)
}

/**
 * Loads `load` whenever `key` changes, showing the last result for the same
 * key meanwhile. A change to `refresh` reloads in place.
 */
@Composable
fun <T> rememberLoaded(key: String, refresh: Any? = null, load: suspend () -> T?): Loaded<T> {
    @Suppress("UNCHECKED_CAST")
    val l = remember(key) { Loaded(PageCache.values[key] as T?) }
    LaunchedEffect(key, refresh) {
        l.loading = true
        try {
            val v = load()
            l.value = v
            l.error = null
            PageCache.values[key] = v
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (l.value == null) l.error = e.message ?: "Something went wrong"
        } finally {
            l.loading = false
        }
    }
    return l
}

// Text input

/**
 * A text field for the remote: focusing it does nothing, pressing OK opens
 * the on-screen keyboard, and Done (or Back) returns to moving around.
 */
@Composable
fun TvTextField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    placeholder: String = "",
    password: Boolean = false,
    keyboard: KeyboardType = KeyboardType.Text,
    onDone: (() -> Unit)? = null,
    width: Dp = 520.dp,
) {
    var editing by remember { mutableStateOf(false) }
    var hadFocus by remember { mutableStateOf(false) }
    val field = remember { FocusRequester() }
    val outer = remember { FocusRequester() }
    val keyboardController = LocalSoftwareKeyboardController.current
    LaunchedEffect(editing) {
        if (editing) {
            awaitFrame()
            runCatching { field.requestFocus() }
            keyboardController?.show()
        }
    }
    fun finish() {
        editing = false
        keyboardController?.hide()
        runCatching { outer.requestFocus() }
    }
    Column(modifier.width(width)) {
        if (label.isNotEmpty()) Text(label, style = MaterialTheme.typography.labelMedium, color = Wren.secondary, modifier = Modifier.padding(start = 4.dp, bottom = 6.dp))
        Box {
            Surface(
                onClick = { editing = true },
                modifier = Modifier.fillMaxWidth().height(56.dp).focusRequester(outer).focusProperties { canFocus = !editing },
                shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(12.dp)),
                colors = ClickableSurfaceDefaults.colors(
                    containerColor = Color.White.copy(alpha = 0.08f), contentColor = Wren.text,
                    focusedContainerColor = Color.White.copy(alpha = 0.95f), focusedContentColor = Color.Black,
                ),
                scale = ClickableSurfaceDefaults.scale(focusedScale = 1.02f),
            ) {
                if (!editing) {
                    val shown = if (password) "•".repeat(value.length) else value
                    Text(
                        shown.ifEmpty { placeholder },
                        style = MaterialTheme.typography.bodyLarge,
                        maxLines = 1, overflow = TextOverflow.Ellipsis,
                        color = if (value.isEmpty()) androidx.tv.material3.LocalContentColor.current.copy(alpha = 0.5f) else androidx.tv.material3.LocalContentColor.current,
                        modifier = Modifier.align(Alignment.CenterStart).padding(horizontal = 18.dp),
                    )
                }
            }
            if (editing) {
                BasicTextField(
                    value = value,
                    onValueChange = onValueChange,
                    singleLine = true,
                    textStyle = MaterialTheme.typography.bodyLarge.copy(color = Wren.text),
                    cursorBrush = SolidColor(Wren.ember),
                    visualTransformation = if (password) PasswordVisualTransformation() else VisualTransformation.None,
                    keyboardOptions = KeyboardOptions(
                        keyboardType = if (password) KeyboardType.Password else keyboard,
                        imeAction = ImeAction.Done,
                        autoCorrectEnabled = false,
                    ),
                    keyboardActions = KeyboardActions(onDone = { finish(); onDone?.invoke() }),
                    modifier = Modifier.fillMaxWidth().height(56.dp)
                        .border(2.dp, Wren.ember, RoundedCornerShape(12.dp))
                        .background(Wren.surfaceHigh, RoundedCornerShape(12.dp))
                        .focusRequester(field)
                        // Compose reports "unfocused" once on attach; only a real focus loss ends editing.
                        .onFocusChanged {
                            if (it.isFocused) hadFocus = true
                            else if (hadFocus && editing) { editing = false; hadFocus = false }
                        },
                    decorationBox = { inner ->
                        Box(Modifier.fillMaxSize().padding(horizontal = 18.dp), contentAlignment = Alignment.CenterStart) { inner() }
                    },
                )
            }
        }
    }
}

// Dialogs

/** A panel over the page; Back closes it. */
@Composable
fun Panel(onDismiss: () -> Unit, title: String, width: Dp = 560.dp, content: @Composable () -> Unit) {
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        // A panel opened by holding OK gets that press's release: it isn't a click on whatever has focus here.
        var armed by remember { mutableStateOf(false) }
        Column(
            Modifier
                .onPreviewKeyEvent {
                    val k = it.nativeKeyEvent
                    if (k.keyCode != android.view.KeyEvent.KEYCODE_DPAD_CENTER && k.keyCode != android.view.KeyEvent.KEYCODE_ENTER &&
                        k.keyCode != android.view.KeyEvent.KEYCODE_NUMPAD_ENTER) return@onPreviewKeyEvent false
                    if (k.action == android.view.KeyEvent.ACTION_DOWN && k.repeatCount == 0) armed = true
                    !armed
                }
                .width(width).heightIn(max = 620.dp).background(Wren.surface, RoundedCornerShape(20.dp))
                .border(1.dp, Wren.line, RoundedCornerShape(20.dp)).padding(28.dp),
        ) {
            Text(title, style = MaterialTheme.typography.headlineMedium, color = Wren.text)
            Spacer(Modifier.height(18.dp))
            content()
        }
    }
}

data class Choice<T>(val value: T, val label: String, val detail: String? = null)

/** Pick one of several options; the current one is checked and focused. */
@Composable
fun <T> ChoicePanel(title: String, choices: List<Choice<T>>, selected: T?, onDismiss: () -> Unit, onPick: (T) -> Unit) {
    Panel(onDismiss, title) {
        val first = remember { FocusRequester() }
        val start = choices.indexOfFirst { it.value == selected }.coerceAtLeast(0)
        LaunchedEffect(Unit) {
            awaitFrame()
            runCatching { first.requestFocus() }
        }
        LazyColumn(verticalArrangement = Arrangement.spacedBy(4.dp), modifier = Modifier.fillMaxWidth()) {
            itemsIndexed(choices) { i, c ->
                ListRow(
                    title = c.label, detail = c.detail, checked = c.value == selected,
                    onClick = { onPick(c.value); onDismiss() },
                    modifier = if (i == start) Modifier.focusRequester(first) else Modifier,
                )
            }
        }
    }
}

@Composable
fun ConfirmPanel(title: String, message: String, confirm: String, onDismiss: () -> Unit, destructive: Boolean = false, onConfirm: () -> Unit) {
    Panel(onDismiss, title) {
        val fr = remember { FocusRequester() }
        LaunchedEffect(Unit) { awaitFrame(); runCatching { fr.requestFocus() } }
        Text(message, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
        Spacer(Modifier.height(24.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            PillButton("Cancel", onDismiss, Modifier.focusRequester(fr))
            PillButton(confirm, { onConfirm(); onDismiss() }, primary = !destructive)
        }
    }
}

/** A row in a list or settings page. */
@Composable
fun ListRow(
    title: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    detail: String? = null,
    value: String? = null,
    checked: Boolean? = null,
    leading: (@Composable () -> Unit)? = null,
    enabled: Boolean = true,
) {
    Surface(
        onClick = onClick,
        enabled = enabled,
        modifier = modifier.fillMaxWidth(),
        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(12.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Color.Transparent, contentColor = Wren.text,
            focusedContainerColor = Color.White, focusedContentColor = Color.Black,
            disabledContainerColor = Color.Transparent, disabledContentColor = Wren.tertiary,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.02f),
    ) {
        Row(Modifier.fillMaxWidth().heightIn(min = 60.dp).padding(horizontal = 18.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            if (checked != null) {
                Box(Modifier.width(22.dp)) { if (checked) Icon(Icons.Rounded.Check, null, Modifier.size(22.dp), tint = Wren.ember) }
            }
            leading?.invoke()
            Column(Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                if (!detail.isNullOrEmpty()) {
                    Text(detail, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis,
                        color = androidx.tv.material3.LocalContentColor.current.copy(alpha = 0.65f))
                }
            }
            if (value != null) {
                Text(value, style = MaterialTheme.typography.bodyMedium, maxLines = 1,
                    color = androidx.tv.material3.LocalContentColor.current.copy(alpha = 0.7f))
            }
        }
    }
}

/** An on/off row. */
@Composable
fun ToggleRow(title: String, checked: Boolean, onChange: (Boolean) -> Unit, modifier: Modifier = Modifier, detail: String? = null) {
    Surface(
        onClick = { onChange(!checked) },
        modifier = modifier.fillMaxWidth(),
        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(12.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Color.Transparent, contentColor = Wren.text,
            focusedContainerColor = Color.White, focusedContentColor = Color.Black,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.02f),
    ) {
        Row(Modifier.fillMaxWidth().heightIn(min = 60.dp).padding(horizontal = 18.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                if (detail != null) Text(detail, style = MaterialTheme.typography.bodySmall,
                    color = androidx.tv.material3.LocalContentColor.current.copy(alpha = 0.65f))
            }
            Box(
                Modifier.size(width = 52.dp, height = 30.dp).background(if (checked) Wren.ember else Color.Gray.copy(alpha = 0.45f), CircleShape).padding(4.dp),
                contentAlignment = if (checked) Alignment.CenterEnd else Alignment.CenterStart,
            ) {
                Box(Modifier.size(22.dp).background(Color.White, CircleShape))
            }
        }
    }
}

/** A titled group of settings rows. */
@Composable
fun FormSection(title: String, footer: String? = null, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth().padding(bottom = 28.dp)) {
        Text(title.uppercase(), style = MaterialTheme.typography.labelSmall, color = Wren.secondary, modifier = Modifier.padding(start = 18.dp, bottom = 8.dp))
        Column(Modifier.fillMaxWidth().background(Wren.surface, RoundedCornerShape(16.dp)).padding(6.dp)) { content() }
        if (footer != null) Text(footer, style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, modifier = Modifier.padding(start = 18.dp, top = 8.dp, end = 18.dp))
    }
}

// QR codes

/** A QR code drawn dark on a white tile, for scanning with a phone. */
@Composable
fun QrCode(content: String, modifier: Modifier = Modifier, size: Dp = 260.dp) {
    val matrix = remember(content) {
        runCatching {
            QRCodeWriter().encode(content, BarcodeFormat.QR_CODE, 0, 0, mapOf(EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.M, EncodeHintType.MARGIN to 0))
        }.getOrNull()
    }
    Box(modifier.size(size).background(Color.White, RoundedCornerShape(16.dp)).padding(16.dp)) {
        if (matrix != null) {
            Canvas(Modifier.fillMaxSize()) {
                val n = matrix.width
                val cell = this.size.minDimension / n
                for (y in 0 until n) for (x in 0 until n) {
                    if (matrix[x, y]) drawRect(Color.Black, Offset(x * cell, y * cell), Size(cell + 0.5f, cell + 0.5f))
                }
            }
        }
    }
}

/** A value bar for simple charts. */
@Composable
fun Bar(fraction: Float, modifier: Modifier = Modifier, color: Color = Wren.ember) {
    Box(modifier.height(10.dp).background(Color.White.copy(alpha = 0.06f), CircleShape)) {
        Box(Modifier.fillMaxHeight().fillMaxWidth(fraction.coerceIn(0.01f, 1f)).background(color, CircleShape))
    }
}
