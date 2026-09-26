package io.github.atlasnotes.ui

import android.app.Activity
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.SystemUpdate
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import io.github.atlasnotes.Updater
import io.github.atlasnotes.Vault
import kotlinx.coroutines.launch

/**
 * A newer version, offered rather than installed.
 *
 * It sits at the top of the list instead of interrupting with a dialog. An
 * update is worth mentioning and never worth blocking someone's notes over,
 * and a dialog on launch is how an app teaches people to dismiss dialogs.
 */
@Composable
fun UpdateCard(release: Vault.Release, onDismiss: () -> Unit) {
    val activity = LocalContext.current as? Activity
    val scope = rememberCoroutineScope()
    var progress by remember { mutableStateOf<Int?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var needsPermission by remember { mutableStateOf(false) }

    Card(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.primaryContainer,
            contentColor = MaterialTheme.colorScheme.onPrimaryContainer,
        ),
    ) {
        Column(Modifier.padding(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.SystemUpdate, contentDescription = null)
                Spacer(Modifier.width(12.dp))
                Text(
                    "Atlas Notes ${release.version} is available",
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )
                IconButton(onClick = onDismiss) {
                    Icon(Icons.Outlined.Close, contentDescription = "Not now")
                }
            }

            if (release.notes.isNotEmpty()) {
                Spacer(Modifier.height(8.dp))
                release.notes.forEach { line ->
                    Text("• $line", style = MaterialTheme.typography.bodyMedium)
                }
            }

            when {
                needsPermission -> {
                    Spacer(Modifier.height(12.dp))
                    Text(
                        "Android needs your permission for Atlas Notes to install " +
                            "an update. It asks once, on its own settings screen.",
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    Spacer(Modifier.height(8.dp))
                    Button(onClick = { activity?.let { Updater.askForPermission(it) } }) {
                        Text("Open that screen")
                    }
                }

                progress != null -> {
                    Spacer(Modifier.height(12.dp))
                    val pct = progress!!
                    if (pct >= 0) {
                        LinearProgressIndicator(
                            progress = { pct / 100f },
                            modifier = Modifier.fillMaxWidth(),
                        )
                        Spacer(Modifier.height(4.dp))
                        Text("Downloading, $pct%", style = MaterialTheme.typography.bodySmall)
                    } else {
                        LinearProgressIndicator(Modifier.fillMaxWidth())
                        Spacer(Modifier.height(4.dp))
                        Text("Downloading", style = MaterialTheme.typography.bodySmall)
                    }
                }

                else -> {
                    Spacer(Modifier.height(12.dp))
                    Button(onClick = {
                        val act = activity ?: return@Button
                        if (!Updater.canInstall(act)) {
                            needsPermission = true
                            return@Button
                        }
                        failed = null
                        progress = -1
                        scope.launch {
                            try {
                                Updater.tidy(act)
                                Updater.install(act, release.version) { progress = it }
                            } catch (e: Exception) {
                                failed = e.message ?: "The update could not be downloaded"
                            } finally {
                                progress = null
                            }
                        }
                    }) {
                        Text("Update")
                    }
                }
            }

            failed?.let {
                Spacer(Modifier.height(8.dp))
                Text(
                    it,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }
}
