package io.github.atlasnotes

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout

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
        // Set again for the next morning: the alarm is a one-shot, so this is
        // what keeps it coming, and it is worked out afresh each time so that
        // the clocks changing cannot leave it an hour off.
        DueReminders.schedule(app)

        // Reading the index is disk work and must not happen on the main
        // thread, which is what onReceive runs on. goAsync keeps the receiver
        // alive until the work is done, within the few seconds the system
        // allows a broadcast.
        val pending = goAsync()
        CoroutineScope(SupervisorJob() + Dispatchers.Default).launch {
            try {
                // The system gives a broadcast only a short time, and a vault
                // that hangs, on a shared folder that has gone away for one,
                // would otherwise be held until it kills the process.
                withTimeout(8_000) { DueReminders.remind(app) }
            } catch (e: Throwable) {
                // A vault that will not open is the interface's to report when
                // someone opens it. A reminder that fails stays quiet, and so
                // does one that timed out. Throwable and not Exception, because
                // an Error escaping here would end the process and leave the
                // receiver unfinished.
            } finally {
                pending.finish()
            }
        }
    }
}
