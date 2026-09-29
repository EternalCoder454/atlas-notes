package io.github.atlasnotes

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * Sets the daily reminder again after the phone restarts, the app is updated,
 * or the phone's time zone or clock is changed.
 *
 * Android forgets every alarm when the phone is switched off, and when an app
 * is replaced by a newer build, and the app updates itself, so without this the
 * reminders would stop until the next time someone happened to open it. A new
 * time zone moves nine o'clock without moving the alarm, so the alarm follows.
 */
class BootReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            Intent.ACTION_BOOT_COMPLETED,
            Intent.ACTION_MY_PACKAGE_REPLACED,
            Intent.ACTION_TIMEZONE_CHANGED,
            Intent.ACTION_TIME_CHANGED ->
                DueReminders.schedule(context)
        }
    }
}
