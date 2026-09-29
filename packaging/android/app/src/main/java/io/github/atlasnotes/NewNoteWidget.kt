package io.github.atlasnotes

import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.Context
import android.widget.RemoteViews

/**
 * The home-screen widget: one button that opens a new note.
 *
 * It is a classic widget, built from RemoteViews, on purpose. Nothing in it
 * ever changes, so it needs no state, no updates and none of the library that
 * a newer way of writing widgets would bring along.
 */
class NewNoteWidget : AppWidgetProvider() {

    /**
     * Gives every widget on the screen its button. This is all that is ever
     * needed, because the widget looks the same for as long as it is there.
     */
    override fun onUpdate(
        context: Context,
        appWidgetManager: AppWidgetManager,
        appWidgetIds: IntArray,
    ) {
        val views = RemoteViews(context.packageName, R.layout.widget_new_note)
        views.setOnClickPendingIntent(R.id.widget_new_note, Capture.newNotePendingIntent(context))
        appWidgetManager.updateAppWidget(appWidgetIds, views)
    }
}
