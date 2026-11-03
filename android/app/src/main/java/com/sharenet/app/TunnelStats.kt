package com.sharenet.app

import java.util.concurrent.atomic.AtomicLong

/**
 * Byte counters for the active sharing session. The service samples twice a
 * second so the UI and the notification can show live rates without doing any
 * work of their own.
 */
object TunnelStats {

    private val downBytes = AtomicLong(0)
    private val upBytes = AtomicLong(0)

    @Volatile
    private var downRate = 0.0

    @Volatile
    private var upRate = 0.0

    @Volatile
    private var peakDownRate = 0.0

    @Volatile
    private var peakUpRate = 0.0

    @Volatile
    private var startedAt = 0L

    @Volatile
    private var endedAt = 0L

    private var lastDown = 0L
    private var lastUp = 0L
    private var lastSampleNanos = 0L

    data class Snapshot(
        val downloadBytes: Long,
        val uploadBytes: Long,
        val downloadRate: Double,
        val uploadRate: Double,
        val peakDownloadRate: Double,
        val peakUploadRate: Double,
        val durationMillis: Long,
        val active: Boolean
    )

    fun startSession() {
        synchronized(this) {
            downBytes.set(0)
            upBytes.set(0)
            lastDown = 0
            lastUp = 0
            downRate = 0.0
            upRate = 0.0
            peakDownRate = 0.0
            peakUpRate = 0.0
            startedAt = System.currentTimeMillis()
            endedAt = 0
            lastSampleNanos = System.nanoTime()
        }
    }

    fun stopSession() {
        synchronized(this) {
            endedAt = System.currentTimeMillis()
            downRate = 0.0
            upRate = 0.0
        }
    }

    fun addDown(bytes: Int) {
        if (bytes > 0) downBytes.addAndGet(bytes.toLong())
    }

    fun addUp(bytes: Int) {
        if (bytes > 0) upBytes.addAndGet(bytes.toLong())
    }

    fun sample() {
        synchronized(this) {
            val now = System.nanoTime()
            val elapsed = (now - lastSampleNanos) / 1_000_000_000.0
            if (elapsed < 0.05) return
            lastSampleNanos = now

            val down = downBytes.get()
            val up = upBytes.get()
            downRate = ((down - lastDown) / elapsed).coerceAtLeast(0.0)
            upRate = ((up - lastUp) / elapsed).coerceAtLeast(0.0)
            lastDown = down
            lastUp = up

            if (downRate > peakDownRate) peakDownRate = downRate
            if (upRate > peakUpRate) peakUpRate = upRate
        }
    }

    fun snapshot(): Snapshot {
        val start = startedAt
        val end = if (endedAt == 0L) System.currentTimeMillis() else endedAt
        return Snapshot(
            downloadBytes = downBytes.get(),
            uploadBytes = upBytes.get(),
            downloadRate = downRate,
            uploadRate = upRate,
            peakDownloadRate = peakDownRate,
            peakUploadRate = peakUpRate,
            durationMillis = if (start == 0L) 0 else (end - start).coerceAtLeast(0),
            active = start != 0L && endedAt == 0L
        )
    }

    fun resetDuration() {
        startedAt = 0
        endedAt = 0
    }
}
