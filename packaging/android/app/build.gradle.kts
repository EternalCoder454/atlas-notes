import java.util.regex.Pattern

plugins {
	id("com.android.application")
	id("org.jetbrains.kotlin.android")
	id("org.jetbrains.kotlin.plugin.compose")
}

// The version comes out of the Go source, which is the one place this project
// records it. Astral kept its own copy here and the two drifted: it shipped
// version code 7 calling itself 0.4.3 long after 0.4.3. Reading the real one
// costs four lines and cannot drift.
val atlasVersion: String by lazy {
	val src = rootProject.file("../../internal/app/version.go")
	// An ordinary escaped string, not a raw one: a raw string ending in a quote
	// runs its closing quotes together with the content's, which is a fight with
	// the lexer that this regex does not need to have.
	val m = Pattern.compile("^var version = \"(.*)\"", Pattern.MULTILINE).matcher(src.readText())
	require(m.find()) { "no version found in $src" }
	m.group(1)
}

// Android wants a single integer that only ever increases, which a dotted
// version is not, so the three parts are packed into one: 0.5.10 becomes 510.
val atlasVersionCode: Int by lazy {
	val p = atlasVersion.split(".").map { it.toIntOrNull() ?: 0 }
	p.getOrElse(0) { 0 } * 10000 + p.getOrElse(1) { 0 } * 100 + p.getOrElse(2) { 0 }
}

android {
	namespace = "io.github.atlasnotes"
	compileSdk = 35

	defaultConfig {
		applicationId = "io.github.atlasnotes"
		// Android 7 and up, which is the same floor the Go runtime and the
		// vault's SQLite are comfortable with.
		minSdk = 24
		targetSdk = 35
		versionCode = atlasVersionCode
		versionName = atlasVersion

		// The interface reads these rather than being told at runtime, so an
		// update check on the phone compares against the same number the
		// desktop would.
		buildConfigField("String", "ATLAS_VERSION", "\"$atlasVersion\"")
	}

	// Signing.
	//
	// The key lives in the repository's secrets, not in the repository: this
	// one is public, and a signing key in it would let anyone build something
	// Android treats as an upgrade to Atlas Notes.
	//
	// It is the same key Astral uses, deliberately. Android refuses to replace
	// an app with one signed by a different key, so the key is what makes one
	// build an update to the last rather than a different app wearing its name.
	// A build without the secrets still works and still installs; it just
	// cannot upgrade a release-signed one, so a fork is not broken by this.
	signingConfigs {
		create("release") {
			val store = System.getenv("ANDROID_KEYSTORE_PATH")
			val pass = System.getenv("ANDROID_KEYSTORE_PASSWORD")
			// Both, or neither. A keystore with no password is not a
			// half-configured build to limp through: Gradle fails on the null
			// password with an error about the keystore rather than about the
			// missing secret, which is a confusing way to learn that one
			// secret of the pair was never set.
			if (!store.isNullOrBlank() && !pass.isNullOrBlank() && file(store).exists()) {
				storeFile = file(store)
				storePassword = pass
				// Optional: a keystore holding a single key does not need to be
				// told which one, and a key whose password matches the store's
				// does not need a second.
				keyAlias = System.getenv("ANDROID_KEY_ALIAS")
				keyPassword = System.getenv("ANDROID_KEY_PASSWORD") ?: pass
			}
		}
	}

	buildTypes {
		release {
			isMinifyEnabled = false
			// R8 would otherwise strip the generated bindings, which are only
			// ever reached from Kotlin by name.
			isShrinkResources = false
			val signed = signingConfigs.getByName("release")
			signingConfig = if (signed.storeFile != null) signed else signingConfigs.getByName("debug")
		}
		debug {
			applicationIdSuffix = ".debug"
			versionNameSuffix = "-debug"
		}
	}

	buildFeatures {
		compose = true
		buildConfig = true
	}

	packaging {
		// The bindings ship one .so per architecture and no duplicates worth
		// merging; this just keeps the licence files out of the APK.
		resources.excludes += setOf("META-INF/*.kotlin_module", "META-INF/LICENSE*")
	}

	compileOptions {
		sourceCompatibility = JavaVersion.VERSION_17
		targetCompatibility = JavaVersion.VERSION_17
	}
	kotlinOptions { jvmTarget = "17" }
}

dependencies {
	// The Go core, as an .aar built by gomobile. It is not checked in: it is a
	// build product of the Go source in this repository, and `make android-aar`
	// or the release workflow produces it.
	implementation(files("libs/atlasbridge.aar"))

	implementation("androidx.core:core-ktx:1.15.0")
	implementation("androidx.activity:activity-compose:1.9.3")
	implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.7")

	val compose = platform("androidx.compose:compose-bom:2024.11.00")
	implementation(compose)
	implementation("androidx.compose.ui:ui")
	implementation("androidx.compose.ui:ui-graphics")
	implementation("androidx.compose.material3:material3")
	// The icon set, which is Material Symbols: the same family the desktop
	// installs, so the two do not disagree about what a padlock looks like.
	implementation("androidx.compose.material:material-icons-extended")
	debugImplementation("androidx.compose.ui:ui-tooling")
	implementation("androidx.compose.ui:ui-tooling-preview")
}
