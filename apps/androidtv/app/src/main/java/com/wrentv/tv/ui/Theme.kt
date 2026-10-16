package com.wrentv.tv.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Typography
import androidx.tv.material3.darkColorScheme

/** The Wren palette. */
object Wren {
    val ember = Color(0xFFE04A2A)
    val ink = Color(0xFF1C1A21)
    val cream = Color(0xFFF7F1E8)
    val background = Color(0xFF0E0C11)
    val surface = Color(0xFF17151B)
    val surfaceHigh = Color(0xFF221F27)
    val line = Color(0x1FFFFFFF)
    val text = Color(0xFFF4F1EC)
    val secondary = Color(0xA6F4F1EC)
    val tertiary = Color(0x66F4F1EC)
    val glass = Color(0x33FFFFFF)
    val good = Color(0xFF4CC38A)
    val bad = Color(0xFFFF6B5A)
}

private val type = Typography(
    displayLarge = TextStyle(fontSize = 54.sp, fontWeight = FontWeight.Bold, lineHeight = 60.sp),
    displayMedium = TextStyle(fontSize = 44.sp, fontWeight = FontWeight.Bold, lineHeight = 50.sp),
    displaySmall = TextStyle(fontSize = 34.sp, fontWeight = FontWeight.Bold, lineHeight = 40.sp),
    headlineLarge = TextStyle(fontSize = 30.sp, fontWeight = FontWeight.SemiBold, lineHeight = 36.sp),
    headlineMedium = TextStyle(fontSize = 26.sp, fontWeight = FontWeight.SemiBold, lineHeight = 32.sp),
    headlineSmall = TextStyle(fontSize = 22.sp, fontWeight = FontWeight.SemiBold, lineHeight = 28.sp),
    titleLarge = TextStyle(fontSize = 20.sp, fontWeight = FontWeight.SemiBold, lineHeight = 26.sp),
    titleMedium = TextStyle(fontSize = 17.sp, fontWeight = FontWeight.Medium, lineHeight = 22.sp),
    titleSmall = TextStyle(fontSize = 15.sp, fontWeight = FontWeight.Medium, lineHeight = 20.sp),
    bodyLarge = TextStyle(fontSize = 17.sp, lineHeight = 25.sp),
    bodyMedium = TextStyle(fontSize = 15.sp, lineHeight = 21.sp),
    bodySmall = TextStyle(fontSize = 13.sp, lineHeight = 18.sp),
    labelLarge = TextStyle(fontSize = 15.sp, fontWeight = FontWeight.SemiBold, lineHeight = 20.sp),
    labelMedium = TextStyle(fontSize = 13.sp, fontWeight = FontWeight.SemiBold, lineHeight = 18.sp),
    labelSmall = TextStyle(fontSize = 11.sp, fontWeight = FontWeight.Bold, lineHeight = 14.sp, letterSpacing = 0.6.sp),
)

@Composable
fun WrenTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = Wren.cream,
            onPrimary = Color.Black,
            secondary = Wren.ember,
            background = Wren.background,
            surface = Wren.surface,
            onSurface = Wren.text,
            onBackground = Wren.text,
            surfaceVariant = Wren.surfaceHigh,
            onSurfaceVariant = Wren.secondary,
            border = Wren.line,
            error = Wren.bad,
        ),
        typography = type,
    ) {
        // Text outside a Surface would otherwise default to black.
        CompositionLocalProvider(androidx.tv.material3.LocalContentColor provides Wren.text, content = content)
    }
}
