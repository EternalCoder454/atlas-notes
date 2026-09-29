package io.github.atlasnotes

import android.app.Application
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * [runCatching], except that being cancelled is not a failure to catch.
 *
 * A coroutine that is cancelled learns of it as a [CancellationException] at
 * its next suspension, and one that catches that and carries on has ignored the
 * request to stop: the job that was replaced or dropped goes on, and may
 * report an error nobody caused. Everything else is caught as before.
 */
private inline fun <T> attempt(block: () -> T): Result<T> =
    runCatching(block).onFailure { if (it is CancellationException) throw it }

/**
 * What the interface is currently showing, and everything that changes it.
 *
 * This is a ViewModel rather than state remembered in a composable so that
 * rotating the phone, or Android briefly taking the screen away, does not throw
 * away an unsaved note.
 */
class VaultModel(app: Application) : AndroidViewModel(app) {

    /** How long typing has to pause before the note is written to disk. */
    private val autosaveDelay = 700L

    var ready by mutableStateOf(false); private set

    /**
     * Whether the vault actually opened. [ready] is set either way, so that
     * the interface stops waiting and can show why, but a request from outside
     * has nowhere to go until this is true.
     */
    private var opened = false
    var notes by mutableStateOf<List<Vault.Note>>(emptyList()); private set
    var query by mutableStateOf(""); private set
    var error by mutableStateOf<String?>(null)

    /** Notes whose text matched the search, as the index last reported. */
    var contentHits by mutableStateOf<Set<String>>(emptySet()); private set

    /**
     * The notes carrying the tag the search box is asking for, as the index
     * last reported, alongside the tag they were found for. The tag is kept so
     * that results still on their way are not mistaken for an empty answer:
     * see [tagSearched].
     */
    private var tagFound by mutableStateOf<Pair<String, List<String>>?>(null)

    /** Where the notes are, once the vault is open. */
    var location by mutableStateOf<Vault.Location?>(null); private set

    /** Whether the sheet about where the notes live is showing. */
    var showFolder by mutableStateOf(false)

    /** The export formats, once asked for, which shows the format sheet. */
    var exportChoices by mutableStateOf<List<Vault.ExportFormat>?>(null)

    fun chooseExport() = viewModelScope.launch {
        exportChoices = attempt { Vault.exportFormats() }.getOrElse {
            error = it.message
            null
        }
    }

    /**
     * Writes the open note, as [format], to the file the user picked. The note
     * is saved first so the file is what is on screen.
     */
    fun exportTo(uri: android.net.Uri, format: Vault.ExportFormat) = viewModelScope.launch {
        val path = openPath ?: return@launch
        try {
            saveJob?.cancel()
            save()
            val bytes = Vault.export(path, format.id)
            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
                getApplication<Application>().contentResolver.openOutputStream(uri)?.use { it.write(bytes) }
                    ?: throw Exception("That place could not be written to")
            }
            error = "Exported as " + format.name
        } catch (e: Vault.Locked) {
            error = "Unlock this note to export it"
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.message ?: "The export failed"
        }
    }

    /** A folder switch is under way; the sheet shows it rather than a second tap. */
    var switching by mutableStateOf(false); private set

    /** The open note, or null when the list is showing. */
    var openPath by mutableStateOf<String?>(null); private set
    var body by mutableStateOf(""); private set
    var tasks by mutableStateOf<List<Vault.Task>>(emptyList()); private set
    var openLocked by mutableStateOf(false); private set
    var saving by mutableStateOf(false); private set

    /** The password sheet, when something needs one. */
    var ask by mutableStateOf<Ask?>(null); private set

    /** A newer version, once the check has found one. */
    var update by mutableStateOf<Vault.Release?>(null); private set

    /**
     * A request for the password.
     *
     * [setting] distinguishes the first time, when the password is being chosen
     * and cannot be recovered, from every time after, when it is being recalled.
     */
    data class Ask(
        val setting: Boolean,
        val purpose: String,
        val onGiven: (String) -> Unit,
    )

    private var saveJob: Job? = null
    private var searchJob: Job? = null
    private var settleJob: Job? = null
    private var captureJob: Job? = null

    /**
     * The newest note a request made, so that a blank request can reuse it
     * while it is still empty. See [reusable]. It is only remembered while the
     * app runs: a note left over from an earlier run is not worth reading the
     * vault at every start to find.
     */
    private var lastCaptured: String? = null

    /**
     * Requests from outside the app that have not been carried out yet, oldest
     * first. Only the main thread touches this, so it needs no lock.
     */
    private val waiting = ArrayDeque<Capture>()

    /**
     * Whether the note that just opened should take the keyboard. It is set
     * for a note made to be typed into and cleared as soon as the editor has
     * asked for focus, so that opening a note later does not also raise it.
     */
    var focusEditor by mutableStateOf(false); private set

    init {
        viewModelScope.launch {
            try {
                // Android's own private directory for this app: not world
                // readable, included in the app's backup, and removed with it.
                // The notes are there too unless the user has chosen a shared
                // folder, which the configuration in it remembers.
                // The reminder opens it by the same name, see Vault.dataDir.
                Vault.open(Vault.dataDir(getApplication<Application>()))
                notes = Vault.notes()
                location = Vault.location()
                opened = true
                ready = true
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = e.message ?: "The vault would not open"
                ready = true
            }
            // A share or a shortcut can arrive while the vault is still
            // opening, which on a cold start it always does. It waited; now
            // it can be carried out.
            runCaptures()
            settleInBackground()
            checkForUpdate()
        }
    }

    /**
     * Called when the app comes back to the front. A sync app may have
     * changed the folder while it was away, so the list is checked against it.
     */
    fun resume() {
        if (ready) settleInBackground()
    }

    /** Runs a settle unless one is already running, then refreshes the list. */
    private fun settleInBackground() {
        if (settleJob?.isActive == true) return
        settleJob = viewModelScope.launch {
            attempt {
                Vault.settle()
                notes = Vault.notes()
                if (query.isNotBlank()) {
                    val tag = tagQuery
                    if (tag != null) searchTag(tag) else searchText(query)
                }
            }
            // Only now can the tasks due be trusted: a folder brought in by a
            // sync app has none in the index until it has been settled.
            offerReminders()
        }
    }

    fun refresh() = viewModelScope.launch {
        attempt { notes = Vault.notes() }.onFailure { error = it.message }
    }

    /**
     * The tag the search box is asking for, without its "#", or null when it
     * holds an ordinary search. A "#" and something after it is a tag, which is
     * what it means in a note too; a lone "#" is the start of typing one.
     */
    val tagQuery: String?
        get() = query.trim()
            .takeIf { it.startsWith("#") }
            ?.substring(1)?.trim()
            ?.takeIf { it.isNotEmpty() }

    /**
     * Whether the tag search has answered for the tag now in the box. Until
     * it has, an empty list means the answer is still coming and not that no
     * note has the tag, so the list must not say so yet.
     */
    val tagSearched: Boolean
        get() = tagQuery.let { it != null && tagFound?.first == it }

    /**
     * Changes the search. Names are matched as the text changes; the text of
     * the notes is asked for once typing pauses, because asking the index on
     * every letter would cost a query per letter for nothing.
     *
     * A tag is asked for instead of, not as well as, the names and the text:
     * someone who types "#recipes" wants the notes tagged so, and a note that
     * merely says the word would only bury them.
     */
    fun updateQuery(text: String) {
        query = text
        searchJob?.cancel()
        val tag = tagQuery
        if (tag != null) {
            contentHits = emptySet()
            searchJob = viewModelScope.launch {
                delay(150)
                searchTag(tag)
            }
            return
        }
        tagFound = null
        if (text.trim().length < 3) {
            contentHits = emptySet()
            return
        }
        searchJob = viewModelScope.launch {
            delay(150)
            searchText(text)
        }
    }

    private suspend fun searchText(text: String) {
        val found = attempt { Vault.searchText(text.trim()) }.getOrDefault(emptySet())
        // The query may have moved on while the index was being asked, and
        // hits for words no longer in the box would show notes that do not
        // match what is there.
        if (tagQuery == null && query.trim() == text.trim()) contentHits = found
    }

    private suspend fun searchTag(tag: String) {
        val found = attempt { Vault.notesWithTag(tag) }.getOrDefault(emptyList())
        // The same as for text. Here it also matters that [tagSearched] treats
        // an answer for a tag as the answer for the tag in the box, so one
        // that arrived late would be taken for it.
        if (tagQuery == tag) tagFound = tag to found
    }

    /**
     * The notes the search box is letting through. Those whose name or folder
     * matches come first, newest first, then those found only by their text.
     * A tag search lets through the notes with the tag and nothing else.
     */
    fun visible(): List<Vault.Note> {
        val q = query.trim()
        if (q.isEmpty()) return notes.sortedByDescending { it.modified }
        // A tag search shows exactly the notes the index says carry the tag,
        // whatever their names say.
        if (tagQuery != null) {
            val tagged = tagFound?.second.orEmpty().toSet()
            return notes.filter { it.path in tagged }.sortedByDescending { it.modified }
        }
        val byName = notes.filter {
            it.name.contains(q, ignoreCase = true) || it.folder.contains(q, ignoreCase = true)
        }
        val named = byName.map { it.path }.toSet()
        val byText = notes.filter { it.path in contentHits && it.path !in named }
        return byName.sortedByDescending { it.modified } + byText.sortedByDescending { it.modified }
    }

    /** Whether a note is in the results only because of what it says. */
    fun foundByText(note: Vault.Note): Boolean {
        val q = query.trim()
        return tagQuery == null && q.isNotEmpty() && note.path in contentHits &&
            !note.name.contains(q, ignoreCase = true) && !note.folder.contains(q, ignoreCase = true)
    }

    /**
     * Moves onto another folder of notes, or back to this app's own with "".
     * The open note is saved and closed first, so nothing is written to one
     * folder while the app is already looking at the other.
     */
    fun useFolder(path: String) = viewModelScope.launch {
        switching = true
        try {
            // Held back from starting anything new by [switching], but one
            // already under way is writing a note into the folder being left,
            // and has to be finished before the vault is moved.
            captureJob?.join()
            saveJob?.cancel()
            save()
            openPath = null
            body = ""
            tasks = emptyList()
            Vault.useFolder(path)
            location = Vault.location()
            notes = Vault.notes()
            contentHits = emptySet()
            showFolder = false
            // The switch stopped any settle on the old folder; this one needs
            // its own, once the old one has finished returning.
            settleJob?.join()
            settleInBackground()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.message ?: "That folder could not be used"
        } finally {
            switching = false
            // Anything that arrived during the switch was held back, so that
            // it would land in the folder the person moved to.
            runCaptures()
        }
    }

    // Opening, editing and closing a note.

    fun open(note: Vault.Note) = viewModelScope.launch { load(note.path) }

    private suspend fun load(path: String) {
        focusEditor = false
        try {
            body = Vault.read(path)
            openPath = path
            openLocked = notes.find { it.path == path }?.locked ?: false
            tasks = Vault.tasks(body)
        } catch (e: Vault.Locked) {
            // Not a failure: the note is fine, we just have not been told the
            // password yet. Ask, and pick up where we left off.
            askForPassword("to open this note") { load(path) }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.message ?: "That note would not open"
        }
    }

    fun edit(text: String) {
        body = text
        openPath ?: return
        saveJob?.cancel()
        saveJob = viewModelScope.launch {
            delay(autosaveDelay)
            save()
            tasks = attempt { Vault.tasks(body) }.getOrDefault(tasks)
        }
    }

    /** Writes the open note if there is one. Safe to call when nothing changed. */
    suspend fun save() {
        val path = openPath ?: return
        saving = true
        attempt { Vault.write(path, body) }.onFailure { error = it.message }
        saving = false
        attempt { notes = Vault.notes() }
    }

    /**
     * Writes the open note now, without waiting for the autosave pause. Called
     * when Android takes the screen away, which it may follow by stopping the
     * process entirely.
     */
    fun flush() = viewModelScope.launch {
        saveJob?.cancel()
        save()
    }

    fun close() = viewModelScope.launch {
        saveJob?.cancel()
        save()
        focusEditor = false
        openPath = null
        body = ""
        tasks = emptyList()
        notes = attempt { Vault.notes() }.getOrDefault(notes)
        // The note may have gained or lost the tag that found it.
        tagQuery?.let { searchTag(it) }
    }

    fun toggleTask(line: Int) = viewModelScope.launch {
        attempt {
            body = Vault.toggleTask(body, line)
            tasks = Vault.tasks(body)
            save()
        }.onFailure { error = it.message }
    }

    fun create() = viewModelScope.launch {
        attempt {
            val path = Vault.create("Untitled")
            notes = Vault.notes()
            load(path)
        }.onFailure { error = it.message }
    }

    // Notes asked for from outside the app: a share, the shortcut, the tile
    // and the widget.

    /**
     * Takes a request from outside the app and carries it out as soon as the
     * vault can take it: now if it is open, and otherwise once it is. Nothing
     * is dropped, because a shared text that vanishes because the app was
     * still starting is worse than one that takes a moment.
     *
     * The exception is a share with nothing in it to save, which there is
     * nothing to wait for: it only says so.
     */
    fun capture(request: Capture) {
        if (request.kind == Capture.Kind.UNSUPPORTED) {
            error = Capture.ONLY_TEXT
            return
        }
        waiting.addLast(request)
        runCaptures()
    }

    /**
     * Carries out the waiting requests, one at a time and in the order they
     * came. A second call while one is running does nothing, because the
     * running loop will get to what was added.
     *
     * A request leaves the queue only once its note has been written, see
     * [carryOut]. One that fails stays at the head and stops the loop, so the
     * error is shown once and the request is tried again with the next, and
     * nothing behind it is made ahead of it.
     */
    private fun runCaptures() {
        if (!opened || switching || captureJob?.isActive == true) return
        captureJob = viewModelScope.launch {
            while (opened && !switching) {
                val next = waiting.firstOrNull() ?: break
                try {
                    carryOut(next)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    error = e.message ?: "That note could not be made"
                    break
                }
            }
        }
    }

    /**
     * Makes the note and opens it.
     *
     * The request is taken off the queue as soon as its note is on disk, and
     * not before: a failure earlier than that leaves it at the head to be tried
     * again, and one later than that, in opening the note, must not make the
     * note a second time.
     *
     * The note that was open is written first, because opening another
     * replaces it on screen, and typing that had not yet reached the autosave
     * would go with it. A password prompt left up is dropped for the same
     * reason: answering it would open the note it was for over this one.
     *
     * Nothing here depends on the vault having a password. A new note goes in
     * the root, which is never locked, so it is written and read back in the
     * clear whether or not the vault has been given one.
     */
    private suspend fun carryOut(request: Capture) {
        saveJob?.cancel()
        save()
        ask = null
        if (request.kind == Capture.Kind.TODAY) {
            openToday(request.typing) { waiting.removeFirstOrNull() }
            return
        }
        val path = reusable(request) ?: createNote(request)
        waiting.removeFirstOrNull()
        attempt { notes = Vault.notes() }
        load(path)
        if (openPath == path) focusEditor = request.typing
    }

    /**
     * The note a blank request can use instead of making another: the newest
     * one a request made, if it is still empty. A shortcut, tile or widget
     * that is tapped again, or an app that sends the intent over and over,
     * would otherwise leave an empty note behind each time.
     *
     * Only a request with nothing to put in the note is given one, since one
     * with text has a name and content of its own. A note that cannot be read,
     * because it has since been locked or deleted, is not reused.
     */
    private suspend fun reusable(request: Capture): String? {
        val last = lastCaptured ?: return null
        if (request.body.isNotEmpty()) return null
        val empty = try {
            Vault.read(last).isBlank()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            false
        }
        return last.takeIf { empty }
    }

    /**
     * Opens today's note, which the Go core makes if there is none yet and
     * otherwise finds as it was left. Like any other note it may be in a
     * locked folder, in which case the password is asked for and this is run
     * again, rather than the shortcut ending in an error.
     *
     * [taken] is called once the note exists, or the password has been asked
     * for, which is as far as this request goes.
     */
    private suspend fun openToday(typing: Boolean, taken: () -> Unit = {}) {
        val path = try {
            Vault.dailyNote()
        } catch (e: Vault.Locked) {
            taken()
            askForPassword("to open today's note") { openToday(typing) }
            return
        }
        taken()
        attempt { notes = Vault.notes() }
        load(path)
        if (openPath == path) focusEditor = typing
    }

    /**
     * Makes a note for [request] and writes what it says into it. The name is
     * the Go core's to choose: it gives one that no note has, protected ones
     * included, so nothing here has to look for a free one.
     */
    private suspend fun createNote(request: Capture): String {
        val path = Vault.create(request.title)
        Vault.write(path, request.body)
        lastCaptured = path
        return path
    }

    /** The editor has taken the keyboard, so it is not to be asked for again. */
    fun editorFocused() { focusEditor = false }

    fun rename(to: String) = viewModelScope.launch {
        val from = openPath ?: return@launch
        val trimmed = to.trim()
        if (trimmed.isEmpty() || trimmed == from.substringAfterLast('/')) return@launch
        val folder = from.substringBeforeLast('/', "")
        val target = if (folder.isEmpty()) trimmed else "$folder/$trimmed"
        attempt {
            saveJob?.cancel()
            save()
            Vault.rename(from, target)
            openPath = target
            // Links to the note are rewritten as it is renamed, and that
            // includes any in the note itself, so the text on screen may be out
            // of date. It was saved just before, so nothing typed is lost by
            // reading it again; leaving it would have the next save write the
            // old links back over the new ones.
            attempt {
                body = Vault.read(target)
                tasks = Vault.tasks(body)
            }.onFailure { error = it.message }
            notes = Vault.notes()
        }.onFailure { error = it.message }
    }

    fun deleteOpen() = viewModelScope.launch {
        val path = openPath ?: return@launch
        saveJob?.cancel()
        attempt {
            Vault.delete(path)
            openPath = null
            body = ""
            tasks = emptyList()
            notes = Vault.notes()
        }.onFailure { error = it.message }
    }

    // Locking.

    /**
     * Locks or unlocks the open note, asking for the password first if the
     * vault has not been given it, and choosing one if this is the first time.
     */
    fun toggleLock() = viewModelScope.launch {
        val path = openPath ?: return@launch
        val locked = openLocked
        withPassword(if (locked) "to unlock this note" else "to lock this note") {
            saveJob?.cancel()
            save()
            if (locked) Vault.unlockNote(path) else Vault.lockNote(path)
            openLocked = !locked
            notes = Vault.notes()
        }
    }

    /**
     * Runs [block] once the vault is unlocked, asking for the password if it is
     * not. A vault with no password yet gets one chosen first, because locking
     * a note with nothing to lock it with would silently leave it readable.
     */
    private suspend fun withPassword(purpose: String, block: suspend () -> Unit) {
        if (Vault.isUnlocked()) {
            attempt { block() }.onFailure { error = it.message }
            return
        }
        askForPassword(purpose) { block() }
    }

    private suspend fun askForPassword(purpose: String, then: suspend () -> Unit) {
        val setting = !Vault.hasPassword()
        ask = Ask(setting, purpose) { entered ->
            viewModelScope.launch {
                try {
                    if (setting) Vault.setPassword(entered) else Vault.unlock(entered)
                    ask = null
                    then()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // Wrong password is the expected case, so it is reported in
                    // the sheet rather than as a failure of the app.
                    error = e.message ?: "That password was not right"
                }
            }
        }
    }

    fun cancelAsk() { ask = null }

    fun dismissError() { error = null }

    // Reminders.

    /**
     * Set when the system's notification prompt should be shown, which is at
     * most once in the life of the install. The activity shows it, since only
     * an activity can, and calls [notificationPromptShown].
     */
    var askNotifications by mutableStateOf(false); private set

    /** Whether the check for it has been made since the app started. */
    private var remindersOffered = false

    /**
     * Decides whether to ask to show notifications, which is only worth doing
     * once there is something to remind of. A prompt on the first launch, for
     * an app that has said nothing yet, is one people refuse, and a refusal is
     * final in a way that being asked again later is not.
     */
    private suspend fun offerReminders() {
        if (remindersOffered) return
        remindersOffered = true
        val app = getApplication<Application>()
        if (!DueReminders.shouldAsk(app)) return
        val due = attempt { Vault.dueTasks(DueReminders.today()) }.getOrDefault(emptyList())
        if (due.isNotEmpty()) askNotifications = true
    }

    /**
     * The prompt is being shown. It is recorded now, before the answer, so that
     * an app killed with the prompt up does not ask a second time.
     */
    fun notificationPromptShown() {
        askNotifications = false
        viewModelScope.launch { DueReminders.markAsked(getApplication<Application>()) }
    }

    // Updates.

    private suspend fun checkForUpdate() {
        attempt { update = Vault.checkUpdate(BuildConfig.ATLAS_VERSION) }
    }

    fun dismissUpdate() { update = null }
}
