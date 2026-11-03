package com.sharenet.app

import android.content.Context
import android.content.SharedPreferences
import java.security.SecureRandom
import java.util.Locale

object Prefs {
    private const val FILE = "sharenet"
    private const val KEY_TOKEN = "token"
    private const val KEY_USB = "usb_enabled"
    private const val KEY_WIFI = "wifi_enabled"
    private const val ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

    fun get(context: Context): SharedPreferences =
        context.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    fun ensureToken(context: Context): String {
        val prefs = get(context)
        val existing = prefs.getString(KEY_TOKEN, null)
        if (!existing.isNullOrBlank()) return existing

        val random = SecureRandom()
        val token = buildString(16) {
            repeat(16) { append(ALPHABET[random.nextInt(ALPHABET.length)]) }
        }
        // commit(), not apply(): the password is generated once for the life of
        // the install and the PC's saved copy has to keep matching it. An async
        // write that is lost with a process kill would silently change it.
        prefs.edit().putString(KEY_TOKEN, token).commit()
        return prefs.getString(KEY_TOKEN, null)?.takeIf { it.isNotBlank() } ?: token
    }

    fun formatToken(token: String): String =
        token.chunked(4).joinToString("-")

    fun normalizeToken(value: String): String =
        value.trim().replace("-", "").replace(" ", "").uppercase(Locale.US)

    fun usbEnabled(context: Context): Boolean = get(context).getBoolean(KEY_USB, true)

    fun wifiEnabled(context: Context): Boolean = get(context).getBoolean(KEY_WIFI, false)

    fun setTransports(context: Context, usb: Boolean, wifi: Boolean) {
        get(context).edit()
            .putBoolean(KEY_USB, usb)
            .putBoolean(KEY_WIFI, wifi)
            .apply()
    }
}
