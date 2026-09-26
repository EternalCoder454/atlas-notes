package io.github.atlasnotes.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.outlined.VisibilityOff
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import io.github.atlasnotes.VaultModel

/**
 * Asking for the password.
 *
 * Two jobs in one sheet, because they are the same question asked at different
 * times: choosing the password the first time, and recalling it afterwards.
 *
 * Choosing it asks twice. The password is not stored anywhere and cannot be
 * recovered by this app or any other, so a typo in it would quietly encrypt a
 * note against a string nobody knows. Confirming costs one field.
 */
@Composable
fun PasswordDialog(ask: VaultModel.Ask, error: String?, onCancel: () -> Unit) {
    var password by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var visible by remember { mutableStateOf(false) }

    val mismatch = ask.setting && confirm.isNotEmpty() && confirm != password
    val ready = password.isNotEmpty() && (!ask.setting || confirm == password)

    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text(if (ask.setting) "Set a password" else "Enter your password") },
        text = {
            Column {
                Text(
                    if (ask.setting) {
                        "This protects the notes you choose to lock. It is not " +
                            "stored anywhere and cannot be recovered: if you " +
                            "forget it, those notes cannot be opened again, by " +
                            "this app or any other."
                    } else {
                        "Needed ${ask.purpose}. It unlocks your protected notes " +
                            "until you close Atlas Notes."
                    },
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(16.dp))
                OutlinedTextField(
                    value = password,
                    onValueChange = { password = it },
                    label = { Text("Password") },
                    singleLine = true,
                    isError = error != null,
                    visualTransformation =
                        if (visible) VisualTransformation.None else PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    trailingIcon = {
                        IconButton(onClick = { visible = !visible }) {
                            Icon(
                                if (visible) Icons.Outlined.VisibilityOff
                                else Icons.Outlined.Visibility,
                                contentDescription =
                                    if (visible) "Hide the password" else "Show the password",
                            )
                        }
                    },
                )
                if (ask.setting) {
                    Spacer(Modifier.height(8.dp))
                    OutlinedTextField(
                        value = confirm,
                        onValueChange = { confirm = it },
                        label = { Text("Password again") },
                        singleLine = true,
                        isError = mismatch,
                        supportingText = if (mismatch) {
                            { Text("These two do not match") }
                        } else null,
                        visualTransformation =
                            if (visible) VisualTransformation.None else PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    )
                }
                if (error != null) {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        error,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { ask.onGiven(password) }, enabled = ready) {
                Text(if (ask.setting) "Set password" else "Unlock")
            }
        },
        dismissButton = { TextButton(onClick = onCancel) { Text("Cancel") } },
    )
}
