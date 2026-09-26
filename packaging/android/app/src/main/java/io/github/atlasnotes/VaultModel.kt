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
    var query by mutableStateOf("")
    var error by mutableStateOf<String?>(null)

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

    init {
        viewModelScope.launch {
            try {
                // Android's own private directory for this app: not world
                // readable, included in the app's backup, and removed with it.
                Vault.open(getApplication<Application>().filesDir.resolve("vault"))
                notes = Vault.notes()
                ready = true
            } catch (e: Exception) {
                error = e.message ?: "The vault would not open"
                ready = true
            }
            checkForUpdate()
        }
    }

    fun refresh() = viewModelScope.launch {
        runCatching { notes = Vault.notes() }.onFailure { error = it.message }
    }

    /** The notes the search box is currently letting through, newest first. */
    fun visible(): List<Vault.Note> {
        val q = query.trim()
        val matching =
            if (q.isEmpty()) notes
            else notes.filter {
                it.name.contains(q, ignoreCase = true) || it.folder.contains(q, ignoreCase = true)
            }
        return matching.sortedByDescending { it.modified }
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
