package io.github.atlasnotes

import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

/**
 * The quick-settings tile: pull down the shade, tap it, and a new note is open.
 *
 * It is a button rather than a switch, so it never shows as on. It only opens
 * the app, and the note is made by MainActivity the same way it is for the
 * shortcut and the widget.
 */
class CaptureTile : TileService() {

    /**
     * Sets the tile's look each time the shade shows it. A tile that has never
     * been told its state can be drawn as unavailable, which would look broken
     * for something that is always ready.
     */
    override fun onStartListening() {
        super.onStartListening()
        qsTile?.apply {
            state = Tile.STATE_INACTIVE
            updateTile()
        }
    }

    /**
     * On a locked phone the tile waits for the unlock first. The notes are
     * private, and a tile on the lock screen should not be a way past it.
     */
    override fun onClick() {
        super.onClick()
        if (isLocked) unlockAndRun { open() } else open()
    }

    /**
     * Opens the note and closes the shade over it. Android 14 wants a
     * PendingIntent for this and deprecates the plain intent, but the plain
     * one is all there is before that.
     */
    private fun open() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startActivityAndCollapse(Capture.newNotePendingIntent(this))
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(Capture.newNoteIntent(this))
        }
    }
}
