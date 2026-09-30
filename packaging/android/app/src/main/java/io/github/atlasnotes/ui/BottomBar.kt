package io.github.atlasnotes.ui

import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.vector.ImageVector
import io.github.atlasnotes.Capture
import io.github.atlasnotes.VaultModel

/**
 * The bar along the bottom of the list and the editor, where a thumb reaches.
 *
 * Back and forward walk the notes that have been open, as in a browser; the
 * rest are the things that used to sit in the top bar or float over the list:
 * search, a new note, today's note, and the sheet for where the notes live.
 * None of them selects anything, because each is an action and not a place, so
 * they are drawn without labels and every one has a description for a screen
 * reader. The bar keeps clear of the gesture area on its own.
 */
@Composable
fun BottomBar(model: VaultModel) {
    NavigationBar(containerColor = MaterialTheme.colorScheme.surfaceContainer) {
        BarItem(IconArrowBack, "Back", model.canGoBack) { model.goBack() }
        BarItem(IconArrowForward, "Forward", model.canGoForward) { model.goForward() }
        BarItem(IconSearch, "Search your notes") { model.search() }
        BarItem(IconAdd, "New note") { model.create() }
        // Goes through the same queue as the shortcut, so it waits for the
        // vault to open and saves the note on screen first.
        BarItem(IconToday, "Today") { model.capture(Capture.today()) }
        BarItem(IconMenu, "Where your notes are") { model.showFolder = true }
    }
}

@Composable
private fun androidx.compose.foundation.layout.RowScope.BarItem(
    icon: ImageVector,
    description: String,
    enabled: Boolean = true,
    onClick: () -> Unit,
) {
    NavigationBarItem(
        selected = false,
        onClick = onClick,
        enabled = enabled,
        icon = { Icon(icon, contentDescription = description) },
        colors = NavigationBarItemDefaults.colors(
            unselectedIconColor = MaterialTheme.colorScheme.onSurfaceVariant,
        ),
    )
}
