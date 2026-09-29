package io.github.atlasnotes

import android.Manifest
import android.app.AlarmManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationChannelCompat
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * The morning reminder of tasks that are due.
 *
 * Once a day at nine, [DueReceiver] asks the vault for the tasks due today or
 * overdue, and if there are any says so in one notification. It reads the index
 * as the app last left it: the index catches up with a folder a sync app has
 * changed whenever the app is opened, so a task dated on a computer is
 * reminded of once it has reached the phone and the app has seen it.
 *
 * The alarm is inexact on purpose. An exact one needs a permission that Android
 * treats as special and asks people to grant in settings, for a nudge that
 * loses nothing by arriving a few minutes late, and an alarm with a window is
 * one the system is willing to batch with others to spare the battery.
 *
 * It is a one-shot alarm that sets the next one when it fires, and not a
 * repeating one: a repeating alarm keeps the interval it was given and not
 * nine o'clock, so after the clocks change it would go on arriving an hour
 * off. Whatever fires it, and the app starting, sets it again.
 */
object DueReminders {

    /** The notification channel. Its name is what the system's settings list. */
    const val CHANNEL = "due_tasks"

    /** The hour, in the phone's own time zone, the reminder comes at. */
    private const val HOUR = 9

    /**
     * How long after nine the system may hold the alarm to fire it with
     * others. Half an hour is far more than the nudge needs to be on time and
     * long enough to be worth batching.
     */
    private const val WINDOW_MILLIS = 30 * 60 * 1000L

    /** How many task texts the notification lists before leaving the rest out. */
    private const val LINES = 3

    /** One notification, replaced in place if it is posted again, never piled up. */
    private const val NOTIFICATION_ID = 1

    private const val PREFS = "reminders"
    private const val ASKED = "asked_for_notifications"

    /**
     * Sets the alarm for the next nine o'clock.
     *
     * It is safe to call as often as is convenient. An alarm for a
     * [PendingIntent] equal to one already set replaces it, so this cannot pile
     * up alarms, and setting it again is what puts it back after anything that
     * cleared it: a restart of the phone, an update of the app, or the phone
     * changing time zone or clock, which moves nine o'clock without telling
     * the alarm. It is also how the alarm carries on, since each one is used
     * up when it fires and [DueReceiver] sets the next.
     *
     * Nothing is set while a notification could not be shown, because waking
     * the phone for a reminder that cannot be delivered is all cost. The alarm
     * is cancelled instead, and is set again when the permission is granted or
     * the app is next started.
     */
    fun schedule(context: Context) {
        val alarms = context.getSystemService(AlarmManager::class.java) ?: return
        val intent = alarmIntent(context)
        if (!canNotify(context)) {
            alarms.cancel(intent)
            return
        }
        alarms.setWindow(AlarmManager.RTC_WAKEUP, nextNine(), WINDOW_MILLIS, intent)
    }

    /**
     * The next nine o'clock by the phone's clock. If it is already past nine
     * today, that is tomorrow's, so that starting the app at noon does not
     * fire the reminder at once and again at nine.
     */
    private fun nextNine(): Long {
        val at = Calendar.getInstance().apply {
            set(Calendar.HOUR_OF_DAY, HOUR)
            set(Calendar.MINUTE, 0)
            set(Calendar.SECOND, 0)
            set(Calendar.MILLISECOND, 0)
        }
        if (at.timeInMillis <= System.currentTimeMillis()) at.add(Calendar.DAY_OF_YEAR, 1)
        return at.timeInMillis
    }

    /**
     * What the alarm sends. It names the receiver outright, so nothing else can
     * receive it, and is immutable because nothing that holds it has any
     * business rewriting it.
     */
    private fun alarmIntent(context: Context): PendingIntent =
        PendingIntent.getBroadcast(
            context,
            0,
            Intent(context, DueReceiver::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

    /**
     * Today's date as the notes write due dates, yyyy-mm-dd.
     *
     * The digits are asked for in a fixed locale. In some languages the default
     * writes them in another script, which would compare as a different date
     * from the one in the note.
     */
    fun today(): String = SimpleDateFormat("yyyy-MM-dd", Locale.ROOT).format(Date())

    /**
     * Whether a notification can be shown at all. Before Android 13 it can
     * unless the person has switched the app's notifications off; from 13 on it
     * also needs the permission, which they may have refused. A phone without
     * it must simply go without, so this is asked before anything is posted.
     */
    fun canNotify(context: Context): Boolean {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
            PackageManager.PERMISSION_GRANTED
        ) {
            return false
        }
        return NotificationManagerCompat.from(context).areNotificationsEnabled()
    }

    /**
     * Whether it is time to ask for the notification permission: Android 13 or
     * later, not yet granted, and never asked before. The system lets an app
     * ask only a couple of times before it stops showing the prompt, and a
     * prompt that comes back after being refused is how an app gets reported,
     * so once is all this does.
     */
    suspend fun shouldAsk(context: Context): Boolean =
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
            PackageManager.PERMISSION_GRANTED &&
            // The first read of a preferences file loads it from disk, which the
            // main thread, where this is called from, must not wait for.
            !withContext(Dispatchers.IO) {
                context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean(ASKED, false)
            }

    /** Remembers that the person has been asked, for good. */
    suspend fun markAsked(context: Context) = withContext(Dispatchers.IO) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean(ASKED, true).apply()
    }

    /**
     * Posts the reminder if there is anything to remind of.
     *
     * The vault is opened if it is not open yet, which it is not when the alarm
     * is what started this process. The Go core keeps one vault per process and
     * opening it again is a no-op, so when the app is running and has it open
     * this simply reuses it.
     */
    suspend fun remind(context: Context) {
        // Asked first, so a phone that cannot show the notification does not
        // open the vault to build one.
        if (!canNotify(context)) return
        Vault.open(Vault.dataDir(context))
        val today = today()
        val due = Vault.dueTasks(today)
        if (due.isEmpty()) return
        post(context, due, today)
    }

    private fun post(context: Context, due: List<Vault.DueTask>, today: String) {
        val manager = NotificationManagerCompat.from(context)
        manager.createNotificationChannel(
            NotificationChannelCompat.Builder(CHANNEL, NotificationManagerCompat.IMPORTANCE_DEFAULT)
                .setName("Due tasks")
                .build(),
        )

        // The desktop words it the same way: one task is either due today or
        // overdue, and saying "due today" of yesterday's would be wrong.
        val title = when {
            due.size == 1 && due[0].due == today -> "1 task due today"
            due.size == 1 -> "1 task overdue"
            else -> "${due.size} tasks due"
        }
        val open = PendingIntent.getActivity(
            context,
            0,
            Intent(context, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val lines = NotificationCompat.InboxStyle().setBigContentTitle(title)
        due.take(LINES).forEach { lines.addLine(it.text) }

        val notification = NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_notification_due)
            .setContentTitle(title)
            .setContentText(due.first().text)
            .setStyle(lines)
            .setContentIntent(open)
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_REMINDER)
            // What the tasks say is private, and a locked phone shows a
            // notification to whoever is holding it. The lock screen gets the
            // count and nothing else.
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
            .setPublicVersion(
                NotificationCompat.Builder(context, CHANNEL)
                    .setSmallIcon(R.drawable.ic_notification_due)
                    .setContentTitle(title)
                    .build(),
            )
            .build()

        try {
            manager.notify(NOTIFICATION_ID, notification)
        } catch (e: SecurityException) {
            // The permission was withdrawn between the check and here. There is
            // nobody to tell, and nothing is lost by going without.
        }
    }
}
