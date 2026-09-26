package io.github.atlasnotes

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.TextButton
import androidx.compose.material3.Text
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import io.github.atlasnotes.ui.AtlasTheme
import io.github.atlasnotes.ui.NoteEditor
import io.github.atlasnotes.ui.NoteList
import io.github.atlasnotes.ui.PasswordDialog

/**
 * The whole app: a list of notes, and one of them open.
 *
 * Everything below the interface is the Go core the desktop app runs, reached
 * through the generated bindings. This class and the composables under ui/ are
 * the only Android-specific code in Atlas Notes.
 */
class MainActivity : ComponentActivity() {

    private val model: VaultModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        setContent {
            AtlasTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    Root(model)
                }
            }
        }
    }

    /**
     * Leaving the app saves the open note.
     *
     * Typing already saves itself after a short pause, so this is for the case
     * where someone types and immediately switches away: Android is within its
     * rights to stop the process at that point, and a note is not something to
     * lose a sentence of.
     */
    override fun onPause() {
        super.onPause()
        model.flush()
    }
}

@Composable
private fun Root(model: VaultModel) {
    val snackbar = remember { SnackbarHostState() }

    // The back gesture closes an open note rather than the app, which is what
    // back means on a screen that shows one thing at a time.
    BackHandler(enabled = model.openPath != null) { model.close() }

    if (model.openPath == null) NoteList(model) else NoteEditor(model)

    model.ask?.let { ask ->
        PasswordDialog(ask, error = model.error, onCancel = { model.cancelAsk() })
    }

    // Failures that are not about the password go to a snackbar; the password
    // sheet shows its own, because that is where the person is looking.
    LaunchedEffect(model.error) {
        val message = model.error
        if (message != null && model.ask == null) {
            snackbar.showSnackbar(message, duration = SnackbarDuration.Short)
            model.dismissError()
        }
    }

    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.BottomCenter) {
        SnackbarHost(snackbar) { data ->
            Snackbar(
                action = {
                    TextButton(onClick = { data.dismiss() }) { Text("Dismiss") }
                },
            ) { Text(data.visuals.message) }
        }
    }
}
