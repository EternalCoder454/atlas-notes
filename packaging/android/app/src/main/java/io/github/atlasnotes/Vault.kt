package io.github.atlasnotes

import io.github.atlasnotes.core.bridge.Bridge
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import java.io.File

/**
 * The vault, as Kotlin sees it.
 *
 * Every call here crosses into the Go core, which is the same code the desktop
 * app runs: the same compressed Markdown files, the same SQLite index, the same
 * encryption. Nothing about the vault format is implemented twice, because two
 * implementations of a format are two chances to disagree about it, and the one
 * that disagrees about encryption loses notes.
 *
 * The crossing is not free and it touches the disk, so all of it happens off
 * the main thread. Callers are suspending functions; Compose never waits.
 */
object Vault {

    /** Thrown when a note is encrypted and the vault has not been unlocked. */
    class Locked : Exception("This note is locked")

    data class Note(
        val path: String,
        val name: String,
        val folder: String,
        val modified: Long,
        val locked: Boolean,
        val hasTasks: Boolean,
    )

    data class Task(val line: Int, val text: String, val checked: Boolean)

    data class Release(val version: String, val notes: List<String>)

    /** Opens the vault in the directory Android gave this app. Idempotent. */
    suspend fun open(dir: File) = io { Bridge.open(dir.absolutePath) }

    suspend fun notes(): List<Note> = io {
        JSONArray(Bridge.listNotes()).map {
            Note(
                path = it.getString("path"),
                name = it.getString("name"),
                folder = it.optString("folder"),
                modified = it.optLong("modified"),
                locked = it.optBoolean("locked"),
                hasTasks = it.optBoolean("hasTasks"),
            )
        }
    }

    suspend fun read(path: String): String = io {
        try {
            Bridge.readNote(path)
        } catch (e: Exception) {
            // The Go side reports this one by message, because an error is all
            // that gomobile carries across. Anything else is a real failure.
            if (e.message == LOCKED) throw Locked() else throw e
        }
    }

    suspend fun write(path: String, content: String) = io { Bridge.writeNote(path, content) }

    suspend fun create(title: String): String = io { Bridge.newNote(title) }

    suspend fun delete(path: String) = io { Bridge.deleteNote(path) }

    suspend fun rename(from: String, to: String) = io { Bridge.renameNote(from, to) }

    // Password protection. The password is passed in and never comes back out:
    // it is not stored, not logged, and not written anywhere on the device.

    suspend fun hasPassword(): Boolean = io { Bridge.hasPassword() }

    suspend fun isUnlocked(): Boolean = io { Bridge.isUnlocked() }

    suspend fun unlock(password: String) = io { Bridge.unlock(password) }

    suspend fun setPassword(password: String) = io { Bridge.setPassword(password) }

    suspend fun lockSession() = io { Bridge.lock() }

    suspend fun lockNote(path: String) = io { Bridge.lockNote(path) }

    suspend fun unlockNote(path: String) = io { Bridge.unlockNote(path) }

    /** The checklist items in a note's text, parsed by the shared Go model. */
    suspend fun tasks(content: String): List<Task> = io {
        JSONArray(Bridge.tasks(content)).map {
            Task(it.getInt("line"), it.getString("text"), it.getBoolean("checked"))
        }
    }

    /** Ticks or unticks one item and returns the whole note text back. */
    suspend fun toggleTask(content: String, line: Int): String =
        io { Bridge.toggleTask(content, line.toLong()) }

    /**
     * Asks whether a newer version has been published, or null if not.
     *
     * This is the same check the desktop makes: one anonymous read of a text
     * file, with nothing about the device or its notes sent. Being offline and
     * being up to date both look like null, which is the right answer to give
     * in both cases.
     */
    suspend fun checkUpdate(version: String): Release? = io {
        val raw = Bridge.checkUpdate(version)
        if (raw.isEmpty()) return@io null
        val o = org.json.JSONObject(raw)
        val notes = o.optJSONArray("notes")
        Release(
            version = o.getString("version"),
            notes = (0 until (notes?.length() ?: 0)).map { notes!!.getString(it) },
        )
    }

    private const val LOCKED = "locked"

    private suspend fun <T> io(block: () -> T): T = withContext(Dispatchers.IO) { block() }

    private fun <T> JSONArray.map(f: (org.json.JSONObject) -> T): List<T> =
        (0 until length()).map { f(getJSONObject(it)) }
}
