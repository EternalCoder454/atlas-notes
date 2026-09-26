package io.github.atlasnotes.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.DeleteOutline
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.LockOpen
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.atlasnotes.VaultModel

/**
 * One note, open.
 *
 * The text is the note: plain Markdown, shown as written. The desktop app hides
 * the markers and renders the formatting in place, and that is not repeated
 * here. It is also, incidentally, where the desktop app's worst crash lived —
 * hidden characters and a live selection disagreeing about where a line ends —
 * so there is no hurry to build a second one on a smaller screen.
 *
 * What a phone does add is checkboxes. Tapping a real checkbox is much easier
 * than putting an x between two brackets with a touch keyboard, so the note's
 * tasks appear above the text and edit it when tapped.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NoteEditor(model: VaultModel) {
    var renaming by remember { mutableStateOf(false) }
    var confirmDelete by remember { mutableStateOf(false) }
    val name = model.openPath?.substringAfterLast('/') ?: ""

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = { model.close() }) {
                        Icon(Icons.AutoMirrored.Outlined.ArrowBack, contentDescription = "Back to the vault")
                    }
                },
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (model.openLocked) {
                            Icon(
                                Icons.Outlined.Lock,
                                contentDescription = "This note is locked",
                                modifier = Modifier.size(18.dp),
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            Spacer(Modifier.width(6.dp))
                        }
                        Text(name, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                },
                actions = {
                    if (model.saving) {
                        CircularProgressIndicator(
                            modifier = Modifier.size(18.dp),
                            strokeWidth = 2.dp,
                        )
                        Spacer(Modifier.width(8.dp))
                    }
                    IconButton(onClick = { renaming = true }) {
                        Icon(Icons.Outlined.DriveFileRenameOutline, contentDescription = "Rename")
                    }
                    IconButton(onClick = { model.toggleLock() }) {
                        Icon(
                            if (model.openLocked) Icons.Outlined.LockOpen else Icons.Outlined.Lock,
                            contentDescription =
                                if (model.openLocked) "Remove the password from this note"
                                else "Protect this note with a password",
                        )
                    }
                    IconButton(onClick = { confirmDelete = true }) {
                        Icon(
                            Icons.Outlined.DeleteOutline,
                            contentDescription = "Delete this note",
                            tint = MaterialTheme.colorScheme.error,
                        )
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.surface,
                ),
            )
        },
    ) { padding ->
        Column(
            Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState()),
        ) {
            if (model.tasks.isNotEmpty()) {
                TaskCard(model)
            }
            TextField(
                value = model.body,
                onValueChange = { model.edit(it) },
                modifier = Modifier.fillMaxWidth().padding(horizontal = 4.dp),
                placeholder = { Text("Start writing") },
                colors = TextFieldDefaults.colors(
                    focusedContainerColor = MaterialTheme.colorScheme.surface,
                    unfocusedContainerColor = MaterialTheme.colorScheme.surface,
                    focusedIndicatorColor = androidx.compose.ui.graphics.Color.Transparent,
                    unfocusedIndicatorColor = androidx.compose.ui.graphics.Color.Transparent,
                ),
                textStyle = MaterialTheme.typography.bodyLarge,
            )
            Spacer(Modifier.height(48.dp))
        }
    }

    if (renaming) {
        RenameDialog(
            current = name,
            onDismiss = { renaming = false },
            onRename = { model.rename(it); renaming = false },
        )
    }

    if (confirmDelete) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            title = { Text("Delete this note?") },
            text = {
                Text(
                    "“$name” will be removed from your vault. " +
                        "This cannot be undone.",
                )
            },
            confirmButton = {
                TextButton(onClick = { confirmDelete = false; model.deleteOpen() }) {
                    Text("Delete", color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmDelete = false }) { Text("Keep it") }
            },
        )
    }
}

/** The note's checklist items, as checkboxes that write back into the text. */
@Composable
private fun TaskCard(model: VaultModel) {
    val done = model.tasks.count { it.checked }
    Card(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceContainer,
        ),
    ) {
        Column(Modifier.padding(vertical = 8.dp)) {
            Text(
                "$done of ${model.tasks.size} done",
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(start = 16.dp, bottom = 4.dp),
            )
            model.tasks.forEach { task ->
                Row(
                    Modifier.fillMaxWidth().padding(end = 12.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Checkbox(
                        checked = task.checked,
                        onCheckedChange = { model.toggleTask(task.line) },
                    )
                    Text(
                        task.text,
                        style = MaterialTheme.typography.bodyMedium,
                        color =
                            if (task.checked) MaterialTheme.colorScheme.onSurfaceVariant
                            else MaterialTheme.colorScheme.onSurface,
                    )
                }
            }
        }
    }
}

@Composable
private fun RenameDialog(current: String, onDismiss: () -> Unit, onRename: (String) -> Unit) {
    var text by remember { mutableStateOf(current) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Rename this note") },
        text = {
            Column {
                Text(
                    "A note's name is its file name, so renaming it renames the " +
                        "file in your vault.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it },
                    singleLine = true,
                    label = { Text("Name") },
                )
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onRename(text) },
                enabled = text.isNotBlank() && text != current,
            ) { Text("Rename") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}
