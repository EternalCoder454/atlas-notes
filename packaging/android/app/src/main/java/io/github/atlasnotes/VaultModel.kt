package io.github.atlasnotes

import android.app.Application
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

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
    var notes by mutableStateOf<List<Vault.Note>>(emptyList()); private set
    var query by mutableStateOf(""); private set
    var error by mutableStateOf<String?>(null)

    /** Notes whose text matched the search, as the index last reported. */
    var contentHits by mutableStateOf<Set<String>>(emptySet()); private set

    /** Where the notes are, once the vault is open. */
    var location by mutableStateOf<Vault.Location?>(null); private set

    /** Whether the sheet about where the notes live is showing. */
    var showFolder by mutableStateOf(false)

    /** The export formats, once asked for, which shows the format sheet. */
    var exportChoices by mutableStateOf<List<Vault.ExportFormat>?>(null)

    fun chooseExport() = viewModelScope.launch {
        exportChoices = runCatching { Vault.exportFormats() }.getOrElse {
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

    init {
        viewModelScope.launch {
            try {
                // Android's own private directory for this app: not world
                // readable, included in the app's backup, and removed with it.
                // The notes are there too unless the user has chosen a shared
                // folder, which the configuration in it remembers.
                Vault.open(getApplication<Application>().filesDir.resolve("vault"))
                notes = Vault.notes()
                location = Vault.location()
                ready = true
            } catch (e: Exception) {
                error = e.message ?: "The vault would not open"
                ready = true
            }
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
            runCatching {
                Vault.settle()
                notes = Vault.notes()
                if (query.isNotBlank()) searchText(query)
            }
        }
    }

    fun refresh() = viewModelScope.launch {
        runCatching { notes = Vault.notes() }.onFailure { error = it.message }
    }

    /**
     * Changes the search. Names are matched as the text changes; the text of
     * the notes is asked for once typing pauses, because asking the index on
     * every letter would cost a query per letter for nothing.
     */
    fun updateQuery(text: String) {
        query = text
        searchJob?.cancel()
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
        contentHits = runCatching { Vault.searchText(text.trim()) }.getOrDefault(emptySet())
    }

    /**
     * The notes the search box is letting through. Those whose name or folder
     * matches come first, newest first, then those found only by their text.
     */
    fun visible(): List<Vault.Note> {
        val q = query.trim()
        if (q.isEmpty()) return notes.sortedByDescending { it.modified }
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
        return q.isNotEmpty() && note.path in contentHits &&
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
        } catch (e: Exception) {
            error = e.message ?: "That folder could not be used"
        } finally {
            switching = false
        }
    }

    // Opening, editing and closing a note.

    fun open(note: Vault.Note) = viewModelScope.launch { load(note.path) }

    private suspend fun load(path: String) {
        try {
            body = Vault.read(path)
            openPath = path
            openLocked = notes.find { it.path == path }?.locked ?: false
            tasks = Vault.tasks(body)
        } catch (e: Vault.Locked) {
            // Not a failure: the note is fine, we just have not been told the
            // password yet. Ask, and pick up where we left off.
            askForPassword("to open this note") { load(path) }
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
            tasks = runCatching { Vault.tasks(body) }.getOrDefault(tasks)
        }
    }

    /** Writes the open note if there is one. Safe to call when nothing changed. */
    suspend fun save() {
        val path = openPath ?: return
        saving = true
        runCatching { Vault.write(path, body) }.onFailure { error = it.message }
        saving = false
        runCatching { notes = Vault.notes() }
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
        openPath = null
        body = ""
        tasks = emptyList()
        notes = runCatching { Vault.notes() }.getOrDefault(notes)
    }

    fun toggleTask(line: Int) = viewModelScope.launch {
        runCatching {
            body = Vault.toggleTask(body, line)
            tasks = Vault.tasks(body)
            save()
        }.onFailure { error = it.message }
    }

    fun create() = viewModelScope.launch {
        runCatching {
            val path = Vault.create("Untitled")
            notes = Vault.notes()
            load(path)
        }.onFailure { error = it.message }
    }

    fun rename(to: String) = viewModelScope.launch {
        val from = openPath ?: return@launch
        val trimmed = to.trim()
        if (trimmed.isEmpty() || trimmed == from.substringAfterLast('/')) return@launch
        val folder = from.substringBeforeLast('/', "")
        val target = if (folder.isEmpty()) trimmed else "$folder/$trimmed"
        runCatching {
            saveJob?.cancel()
            save()
            Vault.rename(from, target)
            openPath = target
            notes = Vault.notes()
        }.onFailure { error = it.message }
    }

    fun deleteOpen() = viewModelScope.launch {
        val path = openPath ?: return@launch
        saveJob?.cancel()
        runCatching {
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
            runCatching { block() }.onFailure { error = it.message }
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

    // Updates.

    private suspend fun checkForUpdate() {
        runCatching { update = Vault.checkUpdate(BuildConfig.ATLAS_VERSION) }
    }

    fun dismissUpdate() { update = null }
}
