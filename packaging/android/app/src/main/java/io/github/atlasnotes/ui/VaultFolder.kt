package io.github.atlasnotes.ui

import android.app.Activity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import io.github.atlasnotes.SharedStorage
import io.github.atlasnotes.VaultModel

/**
 * Where the notes live, and how to have the same ones on a computer.
 *
 * Atlas Notes does not sync anything itself. A sync app does: it keeps one
 * folder the same on the phone and the computer, and Atlas Notes on each
 * reads and writes that folder. This sheet is where the phone is pointed at
 * it.
 */
@Composable
fun VaultFolderDialog(model: VaultModel) {
    val context = LocalContext.current
    val activity = context as? Activity
    var waitingForAccess by remember { mutableStateOf(false) }

    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocumentTree()) { tree ->
        if (tree == null) return@rememberLauncherForActivityResult
        val path = SharedStorage.pathOf(tree)
        if (path == null) {
            model.error = "Choose a folder on this phone's storage. A folder inside a cloud app " +
                "can't be opened as files."
        } else {
            model.useFolder(path)
        }
        Unit
    }
    val askLegacy = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { granted ->
        if (granted.values.all { it }) {
            pick.launch(null)
        } else {
            model.error = "Atlas Notes needs access to the phone's storage to use a folder there."
        }
        Unit
    }

    val choose: () -> Unit = {
        when {
            SharedStorage.canUse(context) -> pick.launch(null)
            SharedStorage.needsSettingsScreen -> {
                // Android 11 and later grant this on a settings page, not in a
                // dialog, so the next step is to come back and choose again.
                waitingForAccess = true
                activity?.let { SharedStorage.openAllFilesAccess(it) }
            }
            else -> askLegacy.launch(SharedStorage.legacyPermissions)
        }
    }

    val location = model.location
    val private = location?.private ?: true

    AlertDialog(
        onDismissRequest = { if (!model.switching) model.showFolder = false },
        title = { Text("Where your notes are") },
        text = {
            Column {
                Text(
                    if (private) "In this app's own storage, which only this phone can see."
                    else "In " + SharedStorage.describe(location?.path ?: ""),
                    style = MaterialTheme.typography.bodyLarge,
                )
                Spacer(Modifier.height(12.dp))
                Text(
                    "To have the same notes on your computer, choose a folder on this phone " +
                        "and keep it in step with your computer's vault folder using a sync " +
                        "app such as Syncthing. Atlas Notes reads and writes the folder " +
                        "directly, and locked notes open with the same password on both.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(8.dp))
                Text(
                    "Notes already in the other place stay where they are. Switching back " +
                        "shows them again.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (waitingForAccess && !SharedStorage.canUse(context)) {
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "Turn on “Allow access to manage all files” for Atlas Notes, " +
                            "then come back and choose the folder.",
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
                if (model.switching) {
                    Spacer(Modifier.height(12.dp))
                    CircularProgressIndicator()
                }
            }
        },
        confirmButton = {
            TextButton(onClick = choose, enabled = !model.switching) {
                Text(if (private) "Choose a folder" else "Choose another folder")
            }
        },
        dismissButton = {
            if (private) {
                TextButton(onClick = { model.showFolder = false }, enabled = !model.switching) {
                    Text("Close")
                }
            } else {
                TextButton(onClick = { model.useFolder("") }, enabled = !model.switching) {
                    Text("Use this app's storage")
                }
            }
        },
    )
}
