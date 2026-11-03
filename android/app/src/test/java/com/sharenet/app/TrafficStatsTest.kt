package com.sharenet.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TrafficStatsTest {

    @Test
    fun `counters track both directions until the session stops`() {
        TunnelStats.startSession()
        TunnelStats.addDown(1_500)
        TunnelStats.addUp(2_048)

        val running = TunnelStats.snapshot()
        assertTrue(running.active)
        assertEquals(1_500, running.downloadBytes)
        assertEquals(2_048, running.uploadBytes)

        TunnelStats.stopSession()
        val stopped = TunnelStats.snapshot()
        assertFalse(stopped.active)
        assertEquals(0.0, stopped.downloadRate, 0.001)
        assertEquals(0.0, stopped.uploadRate, 0.001)
        assertEquals(1_500, stopped.downloadBytes)
    }

    @Test
    fun `formats rates and totals`() {
        assertEquals("512" to "B/s", Format.rate(512.0))
        assertEquals("1.5" to "KB/s", Format.rate(1536.0))
        assertEquals("2.0" to "MB/s", Format.rate(2.0 * 1024 * 1024))
        assertEquals("1.5 GB", Format.bytes(1610612736))
        assertEquals("00:42", Format.duration(42_000))
        assertEquals("1:02:03", Format.duration(3_723_000))
    }
}
