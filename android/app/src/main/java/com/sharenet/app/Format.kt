package com.sharenet.app

import java.util.Locale

object Format {

    private val locale: Locale = Locale.US

    fun rate(bytesPerSecond: Double): Pair<String, String> {
        val value = bytesPerSecond.coerceAtLeast(0.0)
        return when {
            value < 1024 -> pattern(value, value < 100) to "B/s"
            value < 1024 * 1024 -> pattern(value / 1024, value / 1024 < 100) to "KB/s"
            value < 1024.0 * 1024 * 1024 -> pattern(value / (1024 * 1024), value / (1024 * 1024) < 100) to "MB/s"
            else -> String.format(locale, "%.2f", value / (1024 * 1024 * 1024)) to "GB/s"
        }
    }

    fun bytes(count: Long): String {
        var value = count.coerceAtLeast(0).toDouble()
        val units = arrayOf("B", "KB", "MB", "GB", "TB")
        var index = 0
        while (value >= 1024 && index < units.lastIndex) {
            value /= 1024
            index++
        }
        return if (index == 0) {
            String.format(locale, "%.0f %s", value, units[index])
        } else {
            String.format(locale, "%.1f %s", value, units[index])
        }
    }

    fun duration(millis: Long): String {
        val total = millis / 1000
        val hours = total / 3600
        val minutes = (total % 3600) / 60
        val seconds = total % 60
        return if (hours > 0) {
            String.format(locale, "%d:%02d:%02d", hours, minutes, seconds)
        } else {
            String.format(locale, "%02d:%02d", minutes, seconds)
        }
    }

    private fun pattern(value: Double, oneDecimal: Boolean): String =
        if (oneDecimal) String.format(locale, "%.1f", value)
        else String.format(locale, "%.0f", value)
}
