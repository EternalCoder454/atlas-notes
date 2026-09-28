package io.github.atlasnotes

import android.Manifest
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.DocumentsContract
import android.provider.Settings
import androidx.core.content.ContextCompat

/**
 * Using a folder on the phone's shared storage as the vault.
 *
 * The notes are read and written by the Go core with ordinary file paths, the
 * same code the desktop runs. Android's folder picker does not hand out paths:
 * it hands out a content URI, which only Java can open. So a folder the user
 * picks is turned back into the path it names, and the app needs permission to
 * open files there directly, which Android grants in two different ways
 * depending on its version.
 *
 * That permission is what a sync app such as Syncthing uses as well, and for
 * the same reason: both need to see the folder as files.
 */
object SharedStorage {

    /** The permissions Android 10 and earlier use for the same access. */
    val legacyPermissions = arrayOf(
        Manifest.permission.READ_EXTERNAL_STORAGE,
        Manifest.permission.WRITE_EXTERNAL_STORAGE,
    )

    /** Whether the app may open files in shared storage directly. */
    fun canUse(context: Context): Boolean =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            Environment.isExternalStorageManager()
        } else {
            legacyPermissions.all {
                ContextCompat.checkSelfPermission(context, it) == PackageManager.PERMISSION_GRANTED
            }
        }

    /** Whether asking takes a trip to a settings screen rather than a dialog. */
    val needsSettingsScreen: Boolean
        get() = Build.VERSION.SDK_INT >= Build.VERSION_CODES.R

    /**
     * Opens the screen where Android 11 and later grant "All files access".
     * There is no dialog for it: Android keeps it on a settings page, per app.
     */
    fun openAllFilesAccess(activity: Activity) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) return
        try {
            activity.startActivity(
                Intent(
                    Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                    Uri.parse("package:${activity.packageName}"),
                ),
            )
        } catch (e: ActivityNotFoundException) {
            // Some phones only have the list of every app, not the page for one.
            activity.startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
        }
    }

    /**
     * The file path a folder picked in Android's picker stands for, or null
     * when it is not a folder on the phone's own storage: a cloud app's folder
     * has no path, and the notes in it cannot be opened as files.
     */
    fun pathOf(tree: Uri): String? {
        if (tree.authority != "com.android.externalstorage.documents") return null
        val id = DocumentsContract.getTreeDocumentId(tree) // "primary:Documents/Notes"
        val volume = id.substringBefore(':')
        val relative = id.substringAfter(':', "")
        val base =
            if (volume.equals("primary", ignoreCase = true)) {
                @Suppress("DEPRECATION")
                Environment.getExternalStorageDirectory().path
            } else {
                "/storage/$volume" // a memory card, named by its volume id
            }
        return if (relative.isEmpty()) base else "$base/$relative"
    }

    /** A path as a person would say it, without the parts that mean nothing to them. */
    fun describe(path: String): String {
        @Suppress("DEPRECATION")
        val internal = Environment.getExternalStorageDirectory().path
        return when {
            path == internal -> "Internal storage"
            path.startsWith("$internal/") -> "Internal storage/" + path.removePrefix("$internal/")
            else -> path
        }
    }
}
