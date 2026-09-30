package io.github.atlasnotes

import android.Manifest
import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
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
import io.github.atlasnotes.ui.VaultFolderDialog
import io.github.atlasnotes.ui.ExportDialog

/**
 * The whole app: a list of notes, and one of them open.
 *
 * Everything below the interface is the Go core the desktop app runs, reached
 * through the generated bindings. This class and the composables under ui/ are
 * the only Android-specific code in Atlas Notes.
 */
class MainActivity : ComponentActivity() {

    private val model: VaultModel by viewModels()

    /**
     * The system's prompt for permission to show notifications. The reminder
     * checks the permission itself each time it would post, so a refusal, or a
     * grant later in settings, both just work. The alarm is only set while a
     * notification can be shown, though, so the answer is where it gets set
     * after a grant, rather than at the next start of the app.
     */
    private val notificationPermission =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) {
            DueReminders.schedule(this)
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // Every start, and not only the first: Android drops alarms in some
        // cases the app is not told about, and setting one that already exists
        // only replaces it.
        DueReminders.schedule(this)
        // Only a fresh start has a request to act on. After the process has
        // been killed and the activity restored, Android hands back the intent
        // it was first started with, and the note it asked for already exists.
        if (savedInstanceState == null) takeCapture(intent)
        setContent {
            // The model says when the prompt is due; only an activity can show
            // it. It is marked as shown first, so that it is never asked twice.
            LaunchedEffect(model.askNotifications) {
                if (model.askNotifications) {
                    model.notificationPromptShown()
                    notificationPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                }
            }
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
     * A share, a shortcut, the tile or the widget reaching the app while it is
     * already running. The activity is single-task, so Android brings it forward
     * and delivers the intent here instead of starting a second copy.
     *
     * It is also made the activity's own intent, so the next thing that reads
     * [getIntent] sees this one, already emptied, and not the one from launch.
     */
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        takeCapture(intent)
    }

    /**
     * Passes on what [intent] asks for, if it asks for anything, and empties it.
     * The action is cleared so that recreating the activity, which hands back
     * the same intent, does not make the note a second time.
     */
    private fun takeCapture(intent: Intent) {
        Capture.take(intent)?.let { model.capture(it) }
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

    /**
     * Coming back to the app checks the folder for changes, because a sync app
     * may have brought in new notes while it was away.
     */
    override fun onResume() {
        super.onResume()
        model.resume()
    }
}

@Composable
private fun Root(model: VaultModel) {
    val snackbar = remember { SnackbarHostState() }

    // The back gesture is the bar's Back: from a note it goes to the one opened
    // before it, or to the list from the first, and never straight out of the app.
    BackHandler(enabled = model.openPath != null) { model.goBack() }

    if (model.openPath == null) NoteList(model) else NoteEditor(model)

    model.ask?.let { ask ->
        PasswordDialog(ask, error = model.error, onCancel = { model.cancelAsk() })
    }

    if (model.showFolder) {
        VaultFolderDialog(model)
    }

    // Kept mounted while a note is open, so the save picker it launches can
    // report back after the format sheet has closed.
    if (model.openPath != null) {
        ExportDialog(model)
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
