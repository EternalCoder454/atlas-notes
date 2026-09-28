package io.github.atlasnotes.ui

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContract
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import io.github.atlasnotes.Vault
import io.github.atlasnotes.VaultModel

/**
 * Android's "save as": the system's own picker, which asks where and under what
 * name, and hands back somewhere to write. The type has to be given when it is
 * opened, and differs by format, which the stock contract cannot do.
 */
private class CreateTypedDocument : ActivityResultContract<Pair<String, String>, Uri?>() {
    override fun createIntent(context: Context, input: Pair<String, String>): Intent =
        Intent(Intent.ACTION_CREATE_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType(input.second)
            .putExtra(Intent.EXTRA_TITLE, input.first)

    override fun parseResult(resultCode: Int, intent: Intent?): Uri? =
        if (resultCode == Activity.RESULT_OK) intent?.data else null
}

/** Choosing a format, then where the exported note goes. */
@Composable
fun ExportDialog(model: VaultModel) {
    // Registered before anything can return early: the sheet closes as the
    // picker opens, and a launcher that went with it would lose the answer.
    var chosen by remember { mutableStateOf<Vault.ExportFormat?>(null) }
    val create = rememberLauncherForActivityResult(CreateTypedDocument()) { uri ->
        val format = chosen
        if (uri != null && format != null) model.exportTo(uri, format)
        Unit
    }
    val path = model.openPath ?: return
    val choices = model.exportChoices ?: return

    AlertDialog(
        onDismissRequest = { model.exportChoices = null },
        title = { Text("Export this note") },
        text = {
            Column {
                Text(
                    if (model.openLocked) {
                        "This note is protected, but the exported copy will not be: " +
                            "anyone who can open the file can read it."
                    } else {
                        "Word and OpenDocument files open in Microsoft Word, LibreOffice and Google Docs."
                    },
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                choices.forEach { f ->
                    TextButton(
                        onClick = {
                            chosen = f
                            model.exportChoices = null
                            create.launch(Vault.exportFileName(path, f.id) to f.mime)
                        },
                        modifier = Modifier.fillMaxWidth(),
                    ) { Text(f.name) }
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = { model.exportChoices = null }) { Text("Cancel") } },
    )
}
