package com.wrentv.phone.ui

import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.Color

/** The Wren palette. */
object Wren {
    val ember = Color(0xFFE04A2A)
    val background = Color(0xFF0E0C11)
    val surface = Color(0xFF17151B)
    val surfaceHigh = Color(0xFF221F27)
    val cream = Color(0xFFF7F1E8)
    val text = Color(0xFFF4F1EC)
    val secondary = Color(0xFFA9A4AE)
}

@Composable
fun WrenTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = Wren.ember,
            onPrimary = Color.White,
            secondary = Wren.cream,
            background = Wren.background,
            onBackground = Wren.text,
            surface = Wren.background,
            onSurface = Wren.text,
            surfaceVariant = Wren.surfaceHigh,
            onSurfaceVariant = Wren.secondary,
            surfaceContainer = Wren.surface,
            surfaceContainerLow = Wren.surface,
            surfaceContainerHigh = Wren.surfaceHigh,
            surfaceContainerHighest = Wren.surfaceHigh,
            secondaryContainer = Wren.ember.copy(alpha = 0.22f),
            onSecondaryContainer = Color.White,
        ),
    ) {
        // Text outside a Surface reads light, not Material's black.
        CompositionLocalProvider(LocalContentColor provides Wren.text, content = content)
    }
}
