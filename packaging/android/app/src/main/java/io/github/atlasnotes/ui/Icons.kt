package io.github.atlasnotes.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.unit.dp

/**
 * The icons the app draws.
 *
 * These are the project's own Material Symbols -- the same files the desktop
 * embeds, from assets/icons-src -- so a padlock or a checklist means the same
 * thing and looks the same on both.
 *
 * They are defined here rather than taken from material-icons-extended on
 * purpose. That library carries every Material icon there is, some two thousand
 * of them, and this app draws nine. It was the bulk of a 32 MB classes.dex and
 * a large part of every build. The handful of shapes below cost nothing.
 *
 * Material Symbols are drawn in a 960-unit box whose origin sits at the bottom
 * left rather than the top left, which is why each one is wrapped in a group
 * shifted down by its own height. Copying the path data and leaving that out
 * draws every icon just off the top of its own bounds.
 */
private fun symbol(name: String, pathData: String): ImageVector =
    ImageVector.Builder(
        name = name,
        defaultWidth = 24.dp,
        defaultHeight = 24.dp,
        viewportWidth = 960f,
        viewportHeight = 960f,
    )
        .addGroup(name = name, translationY = 960f)
        .addPath(
            pathData = PathParser().parsePathString(pathData).toNodes(),
            // Icon() tints the whole thing with a colour filter, so what this
            // is set to does not reach the screen; it only has to be opaque.
            fill = SolidColor(Color.Black),
        )
        .clearGroup()
        .build()

val IconAdd: ImageVector by lazy {
    symbol("add", "M444-444H240v-72h204v-204h72v204h204v72H516v204h-72v-204Z")
}

val IconSearch: ImageVector by lazy {
    symbol(
        "search",
        "M765-144 526-383q-30 22-65.79 34.5-35.79 12.5-76.18 12.5Q284-336 214-406t-70-170q0-100 " +
            "70-170t170-70q100 0 170 70t70 170.03q0 40.39-12.5 76.18Q599-464 577-434l239 " +
            "239-51 51ZM384-408q70 0 119-49t49-119q0-70-49-119t-119-49q-70 0-119 49t-49 " +
            "119q0 70 49 119t119 49Z",
    )
}

val IconNote: ImageVector by lazy {
    symbol(
        "description",
        "M336-240h288v-72H336v72Zm0-144h288v-72H336v72ZM263.72-96Q234-96 213-117.15T192-168v-624q0" +
            "-29.7 21.15-50.85Q234.3-864 264-864h312l192 192v504q0 29.7-21.16 50.85Q725.68-96 " +
            "695.96-96H263.72ZM528-624v-168H264v624h432v-456H528ZM264-792v189-189 624-624Z",
    )
}

val IconChecklist: ImageVector by lazy {
    symbol(
        "checklist",
        "M232-216 96-352l51-51 84 85 170-170 52 51-221 221Zm0-312L96-664l51-51 85 85 169-170 52 " +
            "51-221 221Zm296 240v-72h336v72H528Zm0-312v-72h336v72H528Z",
    )
}

val IconLock: ImageVector by lazy {
    symbol(
        "lock",
        "M263.72-96Q234-96 213-117.15T192-168v-384q0-29.7 21.15-50.85Q234.3-624 264-624h24v-96q0" +
            "-79.68 56.23-135.84 56.22-56.16 136-56.16Q560-912 616-855.84q56 56.16 56 135.84v96h24q" +
            "29.7 0 50.85 21.15Q768-581.7 768-552v384q0 29.7-21.16 50.85Q725.68-96 695.96-96H263.72" +
            "Zm.28-72h432v-384H264v384Zm267-141.21q21-21.21 21-51T530.79-411q-21.21-21-51-21T429" +
            "-410.79q-21 21.21-21 51T429.21-309q21.21 21 51 21T531-309.21ZM360-624h240v-96q0-50-35" +
            "-85t-85-35q-50 0-85 35t-35 85v96Zm-96 456v-384 384Z",
    )
}

val IconLockOpen: ImageVector by lazy {
    symbol(
        "lock_open",
        "M264-624h336v-96q0-50-35-85t-85-35q-50 0-85 35t-35 85h-72q0-80 56.23-136 56.22-56 136-56Q" +
            "560-912 616-855.84q56 56.16 56 135.84v96h24q29.7 0 50.85 21.15Q768-581.7 768-552v384q0 " +
            "29.7-21.16 50.85Q725.68-96 695.96-96H263.72Q234-96 213-117.15T192-168v-384q0-29.7 " +
            "21.15-50.85Q234.3-624 264-624Zm0 456h432v-384H264v384Zm267-141.21q21-21.21 21-51T530.79" +
            "-411q-21.21-21-51-21T429-410.79q-21 21.21-21 51T429.21-309q21.21 21 51 21T531-309.21ZM264" +
            "-168v-384 384Z",
    )
}

val IconDelete: ImageVector by lazy {
    symbol(
        "delete",
        "M312-144q-29.7 0-50.85-21.15Q240-186.3 240-216v-480h-48v-72h192v-48h192v48h192v72h-48v479.57Q" +
            "720-186 698.85-165T648-144H312Zm336-552H312v480h336v-480ZM384-288h72v-336h-72v336Zm120 " +
            "0h72v-336h-72v336ZM312-696v480-480Z",
    )
}

val IconRename: ImageVector by lazy {
    symbol(
        "edit",
        "M216-216h51l375-375-51-51-375 375v51Zm-72 72v-153l498-498q11-11 23.84-16 12.83-5 27-5 14.16 " +
            "0 27.16 5t24 16l51 51q11 11 16 24t5 26.54q0 14.45-5.02 27.54T795-642L297-144H144Zm600" +
            "-549-51-51 51 51Zm-127.95 76.95L591-642l51 51-25.95-25.05Z",
    )
}

val IconUpgrade: ImageVector by lazy {
    symbol("arrow_upward", "M444-192v-438L243-429l-51-51 288-288 288 288-51 51-201-201v438h-72Z")
}

val IconFolder: ImageVector by lazy {
    symbol(
        "folder",
        "M168-192q-29 0-50.5-21.5T96-264v-432q0-29.7 21.5-50.85Q139-768 168-768h216l96 96h312q29.7 0 " +
            "50.85 21.15Q864-629.7 864-600v336q0 29-21.15 50.5T792-192H168Zm0-72h624v-336H450l-96-96H168v432Zm0 " +
            "0v-432 432Z",
    )
}

/**
 * A calendar with today marked, for opening the day's note.
 *
 * The project's icon set has none, so this one is drawn by hand in the same
 * 960-unit box and is only straight lines and one circle: the frame with a
 * solid header, the two binder rings, and the day. The frame runs clockwise
 * and the window inside it counter-clockwise, which is what cuts it out; the
 * day runs clockwise again to fill it back in. The same shape, in white on the
 * logo's blue, is drawable/ic_shortcut_today.
 */
val IconToday: ImageVector by lazy {
    symbol(
        "today",
        "M144-768H816V-144H144Z M216-616V-216H744V-616Z " +
            "M552-380a60 60 0 1 1 120 0a60 60 0 1 1-120 0Z " +
            "M288-840H360V-696H288Z M600-840H672V-696H600Z",
    )
}
