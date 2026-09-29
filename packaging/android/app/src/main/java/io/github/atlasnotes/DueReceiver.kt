package io.github.atlasnotes

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

/**
 * The daily alarm arriving: posts the reminder of tasks due, if there are any.
 *
 * The receiver's process may have been started only for this, with no activity
 * and no interface, so nothing here depends on either. It asks the vault for
 * what it needs and reports back to the system when it is done.
 */
class DueReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        val app = context.applicationContext
        // Set again for the next morning. A repeating alarm keeps the interval
        // it was given and not nine o'clock, so after the clocks change it
        // would go on arriving an hour off until the app was next opened.
        DueReminders.schedule(app)

        // Reading the index is disk work and must not happen on the main
        // thread, which is what onReceive runs on. goAsync keeps the receiver
        // alive until the work is done, within the few seconds the system
        // allows a broadcast.
        val pending = goAsync()
        CoroutineScope(SupervisorJob() + Dispatchers.Default).launch {
            try {
                DueReminders.remind(app)
            } catch (e: Exception) {
                // A vault that will not open is the interface's to report when
                // someone opens it. A reminder that fails stays quiet.
            } finally {
                pending.finish()
            }
        }
    }
}
