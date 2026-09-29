package io.github.atlasnotes

import android.app.PendingIntent
import android.content.Context
import android.content.Intent

/**
 * A note someone asked for from outside the app, and what goes in it.
 *
 * There are four ways to ask: sharing text into Atlas Notes, a launcher
 * shortcut, a quick-settings tile and a home-screen widget. The last three all
 * mean "a new note, and let me type", and the first means "a note with this in
 * it", so between them there are only two shapes of one request. The Today
 * button and its own shortcut add a third, which makes no note of its own but
 * opens the day's. MainActivity reads it out of an intent, and VaultModel
 * carries it out once the vault is open.
 *
 * @property title what the note is called. That is its file name, so it has
 *   already been made safe to use as one.
 * @property body what the note says.
 * @property typing whether the keyboard should be up as soon as it opens.
 * @property kind whether the request makes a new note or opens today's.
 */
data class Capture(
    val title: String,
    val body: String,
    val typing: Boolean,
    val kind: Kind = Kind.NOTE,
) {

    /** What carrying the request out means. */
    enum class Kind {
        /** Make a note called [title] holding [body], and open it. */
        NOTE,

        /**
         * Open today's note, making it if there is none yet. The title and the
         * body are not used: the note's name is its date and its first
         * contents come from the daily template, both settled by the Go core.
         */
        TODAY,
    }

    companion object {

        /** The action the shortcut, the tile and the widget all send. */
        const val ACTION_NEW_NOTE = "io.github.atlasnotes.action.NEW_NOTE"

        /** The action the Today shortcut sends. */
        const val ACTION_TODAY = "io.github.atlasnotes.action.TODAY"

        private const val BLANK_TITLE = "Untitled"
        private const val SHARED_TITLE = "Shared note"

        /**
         * The longest a title can be. A title is a file name, and one made of
         * four-byte characters at this length, with the extension and a number
         * after it, still fits the 255 bytes most file systems allow.
         */
        private const val MAX_TITLE = 60

        /**
         * What a file name cannot hold on every system the notes may travel to.
         * A vault kept in a synced folder ends up on Windows, and Android's own
         * shared storage refuses the same characters, so a shared page title
         * such as "Why? | Some Site" would otherwise fail to save.
         */
        private const val UNSAFE = "\\/:*?\"<>|"

        private val SPACES = Regex("\\s+")

        /**
         * An empty note with the keyboard up.
         *
         * It is empty rather than the "# Untitled" heading a new note from the
         * list starts with: the cursor opens at the very top, and a heading
         * would leave the first thing typed above it.
         */
        fun blank() = Capture(BLANK_TITLE, "", typing = true)

        /**
         * Today's note, with the keyboard up as for a new one: someone who asks
         * for it is about to write in it.
         */
        fun today() = Capture("", "", typing = true, kind = Kind.TODAY)

        /**
         * Text shared from another app.
         *
         * The subject names the note when there is one, because it is what the
         * sender chose to call the thing, and a page title is a better name
         * than the address after it. Without one the first line of the text
         * does. Only when neither leaves anything usable is it "Shared note".
         *
         * The subject is added to the top of the text only if the text does not
         * already say it, since many apps put the title in both places.
         */
        fun shared(subject: CharSequence?, text: CharSequence?): Capture {
            val heading = subject?.toString()?.trim().orEmpty()
            val content = text?.toString().orEmpty()
            val title = sequenceOf(heading, firstLine(content))
                .map { fileName(it) }
                .firstOrNull { it.isNotEmpty() } ?: SHARED_TITLE
            return Capture(title, withHeading(heading, content), typing = false)
        }

        private fun withHeading(heading: String, content: String): String = when {
            heading.isEmpty() || content.contains(heading) -> content
            content.isEmpty() -> heading
            else -> heading + "\n\n" + content
        }

        private fun firstLine(text: String): String =
            text.lineSequence().map { it.trim() }.firstOrNull { it.isNotEmpty() }.orEmpty()

        /**
         * [raw] as a file name.
         *
         * Path separators matter most. The Go core reads a title as a path, so
         * a slash would put the note in a folder, which may be a locked one,
         * and a new note is meant to land in the vault root.
         */
        private fun fileName(raw: String): String {
            val plain = raw.map { if (it in UNSAFE || it.isISOControl()) ' ' else it }.joinToString("")
            val tidy = plain.trim().replace(SPACES, " ")
            // A leading dot hides a file and a trailing one is refused on Windows.
            return firstCodePoints(tidy, MAX_TITLE).trim(' ', '.')
        }

        /** The first [count] characters of [text], never cutting an emoji in half. */
        private fun firstCodePoints(text: String, count: Int): String =
            if (text.codePointCount(0, text.length) <= count) text
            else text.substring(0, text.offsetByCodePoints(0, count))

        /**
         * [title], or [title] with a number after it if a note by that name is
         * already in [taken].
         *
         * The Go core does this itself, but it only looks for an ordinary
         * note. A password protected one is stored under another extension and
         * is invisible to it, so a new note given the same name would be
         * written over the protected one. The list of notes knows about both,
         * and names are compared without regard to case because shared storage
         * on Android does.
         */
        fun distinct(title: String, taken: Collection<String>): String {
            val used = taken.mapTo(HashSet()) { it.lowercase() }
            var candidate = title
            var number = 2
            while (candidate.lowercase() in used) {
                candidate = "$title $number"
                number++
            }
            return candidate
        }

        /**
         * Reads the request out of [intent], or null if it is not one.
         *
         * The intent is emptied as it is read. Android hands the same intent
         * back to an activity that is recreated, and to one restored after its
         * process died, and without this each of those would make the note
         * again.
         */
        fun take(intent: Intent): Capture? {
            val capture = read(intent) ?: return null
            intent.action = null
            intent.removeExtra(Intent.EXTRA_TEXT)
            intent.removeExtra(Intent.EXTRA_SUBJECT)
            return capture
        }

        private fun read(intent: Intent): Capture? = when (intent.action) {
            ACTION_NEW_NOTE -> blank()
            ACTION_TODAY -> today()
            Intent.ACTION_SEND ->
                if (intent.type.orEmpty().startsWith("text/")) {
                    shared(
                        intent.getCharSequenceExtra(Intent.EXTRA_SUBJECT),
                        intent.getCharSequenceExtra(Intent.EXTRA_TEXT),
                    )
                } else {
                    null
                }
            else -> null
        }

        /**
         * The intent that starts a new note.
         *
         * It names the activity outright rather than going by the action, so it
         * reaches this build of the app and not another one that answers to the
         * same action. It asks for a new task because a quick-settings tile
         * starts it from a service, which Android refuses otherwise; the
         * activity is single-task anyway, so the running app is brought
         * forward rather than a second one made.
         */
        fun newNoteIntent(context: Context): Intent =
            Intent(ACTION_NEW_NOTE)
                .setClass(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

        /**
         * [newNoteIntent] for a widget or tile to hold on to. It is immutable
         * because nothing that receives it has any business rewriting it.
         */
        fun newNotePendingIntent(context: Context): PendingIntent =
            PendingIntent.getActivity(
                context,
                0,
                newNoteIntent(context),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
    }
}
