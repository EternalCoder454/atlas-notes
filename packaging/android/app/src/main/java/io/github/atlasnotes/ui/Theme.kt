package io.github.atlasnotes.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

/**
 * The theme: Material 3, in Atlas Notes' own purple.
 *
 * Both schemes are built around #9a57e3, the accent the desktop app uses, so
 * the phone looks like the same application rather than a relative of it. That
 * is the reason dynamic colour is not used here: Material You would repaint the
 * app to match each phone's wallpaper, which is a fine default for something
 * with no identity of its own and the wrong one for something that also runs on
 * a desktop next to it.
 *
 * Light and dark follow the system, with no toggle. A notes app is not the right
 * place to keep a second opinion about whether it is night.
 */

private val AtlasLight = lightColorScheme(
    primary = Color(0xFF6E43B0),
    onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFEBDDFF),
    onPrimaryContainer = Color(0xFF270D57),
    secondary = Color(0xFF635B70),
    onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFE9DEF8),
    onSecondaryContainer = Color(0xFF1F182B),
    tertiary = Color(0xFF7E5260),
    onTertiary = Color(0xFFFFFFFF),
    background = Color(0xFFFEF7FF),
    onBackground = Color(0xFF1D1A22),
    surface = Color(0xFFFEF7FF),
    onSurface = Color(0xFF1D1A22),
    surfaceVariant = Color(0xFFE8E0EB),
    // Secondary text. Deliberately darker than Material's default role, which
    // in the desktop app measured about 2.3:1 against the page and failed the
    // contrast floor; this is comfortably past 4.5:1.
    onSurfaceVariant = Color(0xFF494451),
    surfaceContainer = Color(0xFFF3EDF7),
    surfaceContainerHigh = Color(0xFFECE6F0),
    surfaceContainerHighest = Color(0xFFE6E0E9),
    outline = Color(0xFF7A747E),
    outlineVariant = Color(0xFFCBC4CF),
    error = Color(0xFFB3261E),
    onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFF9DEDC),
    onErrorContainer = Color(0xFF410E0B),
)

private val AtlasDark = darkColorScheme(
    primary = Color(0xFFD5BBFF),
    onPrimary = Color(0xFF3D1D74),
    primaryContainer = Color(0xFF55348C),
    onPrimaryContainer = Color(0xFFEBDDFF),
    secondary = Color(0xFFCDC2DB),
    onSecondary = Color(0xFF342D40),
    secondaryContainer = Color(0xFF4B4358),
    onSecondaryContainer = Color(0xFFE9DEF8),
    tertiary = Color(0xFFEFB8C8),
    onTertiary = Color(0xFF4A2532),
    background = Color(0xFF141218),
    onBackground = Color(0xFFE6E0E9),
    surface = Color(0xFF141218),
    onSurface = Color(0xFFE6E0E9),
    surfaceVariant = Color(0xFF4A454E),
    onSurfaceVariant = Color(0xFFCDC5D0),
    surfaceContainer = Color(0xFF211F26),
    surfaceContainerHigh = Color(0xFF2B2930),
    surfaceContainerHighest = Color(0xFF36343B),
    outline = Color(0xFF968F99),
    outlineVariant = Color(0xFF4A454E),
    error = Color(0xFFF2B8B5),
    onError = Color(0xFF601410),
    errorContainer = Color(0xFF8C1D18),
    onErrorContainer = Color(0xFFF9DEDC),
)

/** The gold a favourited note is starred in, in both schemes. */
val StarGold = Color(0xFFE3A008)

@Composable
fun AtlasTheme(dark: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (dark) AtlasDark else AtlasLight, content = content)
}
