package io.github.atlasnotes.ui

import android.text.format.DateUtils
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.atlasnotes.Vault
import io.github.atlasnotes.VaultModel

/**
 * The vault: everything in it, newest first, with a box to narrow it down.
 *
 * A phone shows one thing at a time, so this is the whole screen and opening a
 * note replaces it. The desktop app's three panes do not survive the trip to a
 * screen this size, and a scaled-down version of them would be worse than the
 * list a phone is actually good at.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NoteList(model: VaultModel) {
    val shown = model.visible()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Atlas Notes") },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                ),
            )
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = { model.create() },
                icon = { Icon(Icons.Filled.Add, contentDescription = null) },
                text = { Text("New note") },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            model.update?.let { rel ->
                UpdateCard(rel, onDismiss = { model.dismissUpdate() })
            }

            SearchField(
                value = model.query,
                onValueChange = { model.query = it },
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
            )

            when {
                !model.ready -> Box(Modifier.fillMaxSize(), Alignment.Center) {
                    CircularProgressIndicator()
                }

                shown.isEmpty() -> EmptyState(searching = model.query.isNotBlank())

                else -> LazyColumn(
                    contentPadding = PaddingValues(bottom = 96.dp),
                ) {
                    items(shown, key = { it.path }) { note ->
                        NoteRow(note) { model.open(note) }
                    }
                }
            }
        }
    }
}

@Composable
private fun SearchField(value: String, onValueChange: (String) -> Unit, modifier: Modifier = Modifier) {
    OutlinedTextField(
        value = value,
        onValueChange = onValueChange,
        modifier = modifier.fillMaxWidth(),
        placeholder = { Text("Search your notes") },
        leadingIcon = { Icon(Icons.Outlined.Search, contentDescription = null) },
        trailingIcon = {
            if (value.isNotEmpty()) {
                IconButton(onClick = { onValueChange("") }) {
                    Icon(Icons.Outlined.Close, contentDescription = "Clear the search")
                }
            }
        },
        singleLine = true,
        shape = RoundedCornerShape(28.dp),
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
    )
}

@Composable
private fun NoteRow(note: Vault.Note, onClick: () -> Unit) {
    ListItem(
        headlineContent = {
            Text(note.name, maxLines = 1, overflow = TextOverflow.Ellipsis)
        },
        supportingContent = { Text(subtitle(note)) },
        leadingContent = {
            Icon(
                imageVector = when {
                    note.locked -> Icons.Outlined.Lock
                    note.hasTasks -> Icons.Outlined.Checklist
                    else -> Icons.Outlined.Description
                },
                contentDescription = null,
                tint = if (note.locked) MaterialTheme.colorScheme.onSurfaceVariant
                else MaterialTheme.colorScheme.primary,
            )
        },
        modifier = Modifier.clickable(onClick = onClick),
    )
    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
}

/**
 * The line under a note's name: where it lives and when it last changed.
 *
 * A locked note says so instead of saying when it changed, because the useful
 * thing to know about it is that opening it needs the password.
 */
private fun subtitle(note: Vault.Note): String {
    val when_ =
        if (note.modified <= 0) "never opened"
        else DateUtils.getRelativeTimeSpanString(
            note.modified * 1000L,
            System.currentTimeMillis(),
            DateUtils.MINUTE_IN_MILLIS,
        ).toString()
    val where = note.folder.ifEmpty { null }
    return listOfNotNull(where, if (note.locked) "Locked" else when_).joinToString(" · ")
}

@Composable
private fun EmptyState(searching: Boolean) {
    Box(Modifier.fillMaxSize().padding(32.dp), Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Icon(
                if (searching) Icons.Outlined.Search else Icons.Outlined.Description,
                contentDescription = null,
                modifier = Modifier.size(48.dp),
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(16.dp))
            Text(
                if (searching) "Nothing matches that" else "No notes yet",
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                if (searching) "Try a shorter search." else "Tap New note to start one.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
