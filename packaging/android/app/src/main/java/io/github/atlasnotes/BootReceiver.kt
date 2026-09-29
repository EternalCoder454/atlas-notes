package io.github.atlasnotes

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * Sets the daily reminder again after the phone restarts or the app is updated.
 *
 * Android forgets every alarm when the phone is switched off, and when an app
 * is replaced by a newer build, and the app updates itself, so without this the
 * reminders would stop until the next time someone happened to open it.
 */
class BootReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED ->
                DueReminders.schedule(context)
        }
    }
}
