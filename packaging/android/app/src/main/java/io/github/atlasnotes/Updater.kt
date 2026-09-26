package io.github.atlasnotes

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.net.HttpURLConnection
import java.net.URL

/**
 * Installing a newer Atlas Notes.
 *
 * Android will not update a sideloaded app by itself, so this asks: it fetches
 * the published build and hands it to the system installer, which is the only
 * thing that can actually replace an app.
 *
 * Nothing here is trusted with much, and that is deliberate. The installer shows
 * what it is about to do, and it refuses a build signed with a different key
 * from the one already on the phone. So a file altered on the way across the
 * network is rejected by Android rather than by this code, which is the right
 * place for that decision to be made.
 *
 * This differs from Astral's updater in where the build comes from: Astral pulls
 * it from the PC it is already paired with, and Atlas Notes has no server to ask,
 * so it reads the published release directly.
 */
object Updater {

    /**
     * Where a release lives. The name has to match what the release workflow
     * attaches, because a download of the wrong name is a 404 and an update
     * that silently never arrives.
     */
    private const val ASSET =
        "https://github.com/EternalCoder454/atlas-notes/releases/download/v%1\$s/atlas-notes-%1\$s.apk"

    /**
     * A build that will not plausibly be an Atlas Notes APK. The Go runtime and
     * two architectures put the real one in the tens of megabytes; this is only
     * here so a wrong URL cannot quietly fill the phone's storage.
     */
    private const val sizeCap = 250L * 1024 * 1024

    /** The directory downloads land in, which is the only one the provider shares. */
    private fun dir(activity: Activity) = File(activity.cacheDir, "updates").apply { mkdirs() }

    /**
     * True when this app is allowed to ask the installer. Below Android 8 the
     * permission is granted at install time and there is nothing to check.
     */
    fun canInstall(activity: Activity): Boolean =
        Build.VERSION.SDK_INT < Build.VERSION_CODES.O ||
            activity.packageManager.canRequestPackageInstalls()

    /**
     * Sends the user to the one screen that can grant it. Android does not let
     * an app ask for this in a dialog: it is a settings page, per app.
     */
    fun askForPermission(activity: Activity) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        activity.startActivity(
            Intent(
                Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                Uri.parse("package:${activity.packageName}"),
            ),
        )
    }

    /**
     * Downloads [version] and opens the installer.
     *
     * [onProgress] is called with whole percentages, or -1 when the server did
     * not say how large the file is, so the card can say something while a few
     * tens of megabytes arrive.
     *
     * Throws with a message worth showing someone if it does not work.
     */
    suspend fun install(activity: Activity, version: String, onProgress: (Int) -> Unit) {
        val apk = withContext(Dispatchers.IO) { download(activity, version, onProgress) }
        open(activity, apk)
    }

    private fun download(activity: Activity, version: String, onProgress: (Int) -> Unit): File {
        val conn = (URL(ASSET.format(version)).openConnection() as HttpURLConnection).apply {
            connectTimeout = 15_000
            readTimeout = 60_000
            // GitHub answers a release download with a redirect to storage.
            instanceFollowRedirects = true
        }
        if (conn.responseCode != HttpURLConnection.HTTP_OK) {
            throw Exception(
                when (conn.responseCode) {
                    404 -> "There is no download published for $version yet"
                    else -> "The download answered ${conn.responseCode}"
                },
            )
        }
        val total = conn.contentLengthLong
        if (total > sizeCap) throw Exception("That download is implausibly large; ignoring it")

        // A part file, renamed only once it is whole: an interrupted download
        // must never be handed to the installer as if it were a build.
        val part = File(dir(activity), "atlas-notes-$version.apk.part")
        val done = File(dir(activity), "atlas-notes-$version.apk")
        part.delete()
        conn.inputStream.use { input ->
            part.outputStream().use { out ->
                val buf = ByteArray(64 * 1024)
                var read = 0L
                var last = -2
                while (true) {
                    val n = input.read(buf)
                    if (n < 0) break
                    out.write(buf, 0, n)
                    read += n
                    if (read > sizeCap) {
                        part.delete()
                        throw Exception("That download is implausibly large; stopped it")
                    }
                    val pct = if (total > 0) (read * 100 / total).toInt() else -1
                    if (pct != last) {
                        last = pct
                        onProgress(pct)
                    }
                }
            }
        }
        if (!part.renameTo(done)) {
            part.delete()
            throw Exception("Could not finish the download")
        }
        return done
    }

    /** Hands a downloaded build to the system installer. */
    private fun open(activity: Activity, apk: File) {
        val uri = FileProvider.getUriForFile(activity, "${activity.packageName}.files", apk)
        activity.startActivity(
            Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, "application/vnd.android.package-archive")
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
            },
        )
    }

    /** Removes anything left behind by an earlier update. */
    fun tidy(activity: Activity) {
        dir(activity).listFiles()?.forEach { it.delete() }
    }
}
