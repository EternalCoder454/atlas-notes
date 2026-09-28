plugins {
	id("com.android.application") version "9.4.1" apply false
	// AGP 9 compiles Kotlin itself, so the Kotlin Android plugin is gone. The
	// Compose compiler plugin stays, and its version is the Kotlin version the
	// build uses.
	id("org.jetbrains.kotlin.plugin.compose") version "2.4.20" apply false
}
