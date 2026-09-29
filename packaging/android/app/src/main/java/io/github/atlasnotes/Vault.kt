package io.github.atlasnotes

import android.content.Context
import io.github.atlasnotes.core.bridge.Bridge
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import java.io.File
import java.util.TimeZone

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

    /**
     * An unfinished task that has a due date, and where to find it: the note
     * it is in and its 0-based line there. [priority] is "high", "medium",
     * "low", or empty for none.
     */
    data class DueTask(
        val path: String,
        val line: Int,
        val text: String,
        val due: String,
        val priority: String,
    )

    /**
     * The directory Android gave this app for its vault. The interface and the
     * daily reminder both open the vault, and the Go core keeps one per
     * process, so they have to name the same directory or the second would
     * open a vault the first is not looking at.
     */
    fun dataDir(context: Context): File = context.filesDir.resolve("vault")

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

    /**
     * Today's note, made first if there is none yet. It is a note in a folder
     * that may be locked, so like [read] it throws [Locked] rather than
     * failing when the password is what is missing.
     *
     * The phone's own offset from UTC is passed in, because the Go runtime on
     * Android has no time zone of its own and would name the UTC day. It is
     * read at the moment of asking, so a change of zone or of daylight time is
     * followed.
     */
    suspend fun dailyNote(): String = io {
        val offset = TimeZone.getDefault().getOffset(System.currentTimeMillis()) / 1000
        try {
            Bridge.dailyNote(offset.toLong())
        } catch (e: Exception) {
            if (e.message == LOCKED) throw Locked() else throw e
        }
    }

    /**
     * The unfinished tasks due on or before [through], written yyyy-mm-dd, most
     * pressing first. What is overdue is included. Tasks in locked notes never
     * are.
     */
    suspend fun dueTasks(through: String): List<DueTask> = io {
        JSONArray(Bridge.dueTasks(through)).map {
            DueTask(
                path = it.getString("path"),
                line = it.getInt("line"),
                text = it.getString("text"),
                due = it.getString("due"),
                priority = it.optString("priority"),
            )
        }
    }

    /**
     * Paths of the notes that carry [tag], newest first. The tag may be
     * written with its "#" and in any case.
     */
    suspend fun notesWithTag(tag: String): List<String> = io {
        val a = JSONArray(Bridge.notesWithTag(tag))
        (0 until a.length()).map { a.getString(it) }
    }

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

    /** Where the notes are: a folder, and whether it is this app's own. */
    data class Location(val path: String, val private: Boolean)

    suspend fun location(): Location = io {
        val o = org.json.JSONObject(Bridge.vaultPath())
        Location(o.getString("path"), o.optBoolean("private"))
    }

    /**
     * Moves onto another folder of notes: [path], or this app's own storage
     * when it is empty. The folder left behind is not touched.
     */
    suspend fun useFolder(path: String) = io { Bridge.setVaultPath(path) }

    /**
     * Brings the list up to date with the folder after something else has
     * changed it, a sync app most of all, and reads what it has to for
     * checklists and search. Returns how many notes it read. It can take a
     * few seconds the first time on a large vault, so it is run in the
     * background after the list is already showing.
     */
    suspend fun settle(): Int = io { Bridge.settle().toInt() }

    /** Paths of the notes whose text contains every word of [query]. */
    suspend fun searchText(query: String): Set<String> = io {
        val a = JSONArray(Bridge.searchNotes(query))
        (0 until a.length()).map { a.getString(it) }.toSet()
    }

    /** A kind of document a note can be exported as. */
    data class ExportFormat(val id: String, val name: String, val ext: String, val mime: String)

    suspend fun exportFormats(): List<ExportFormat> = io {
        JSONArray(Bridge.exportFormats()).map {
            ExportFormat(it.getString("id"), it.getString("name"), it.getString("ext"), it.getString("mime"))
        }
    }

    /** A note rendered as [format], ready to be written to a file. */
    suspend fun export(path: String, format: String): ByteArray = io {
        try {
            Bridge.exportNote(path, format)
        } catch (e: Exception) {
            if (e.message == LOCKED) throw Locked() else throw e
        }
    }

    fun exportFileName(path: String, format: String): String = Bridge.exportFileName(path, format)

    private const val LOCKED = "locked"

    private suspend fun <T> io(block: () -> T): T = withContext(Dispatchers.IO) { block() }

    private fun <T> JSONArray.map(f: (org.json.JSONObject) -> T): List<T> =
        (0 until length()).map { f(getJSONObject(it)) }
}
