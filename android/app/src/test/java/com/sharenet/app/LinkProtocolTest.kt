package com.sharenet.app

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.net.InetAddress

class LinkProtocolTest {

    @Test
    fun `udp frame matches the windows wire format`() {
        val address = InetAddress.getByName("203.0.113.9")
        val payload = byteArrayOf(0x01, 0x02, 0x03)

        val output = ByteArrayOutputStream()
        LinkProtocol.writeUdpFrame(output, address, 4444, payload, 0, payload.size)

        val bytes = output.toByteArray()
        // length (2) | ATYP=1 | address (4) | port (2) | payload
        assertEquals(0, bytes[0].toInt())
        assertEquals(10, bytes[1].toInt())
        assertEquals(LinkProtocol.ATYP_IPV4, bytes[2].toInt())
        assertArrayEquals(address.address, bytes.copyOfRange(3, 7))
        assertEquals(4444, ((bytes[7].toInt() and 0xFF) shl 8) or (bytes[8].toInt() and 0xFF))
        assertArrayEquals(payload, bytes.copyOfRange(9, 12))
    }

    @Test
    fun `udp frame from windows parses correctly`() {
        val frame = byteArrayOf(
            0, 12, // frame length
            LinkProtocol.ATYP_IPV4.toByte(),
            1, 2, 3, 4,
            0x1F, 0x90.toByte(), // port 8080
            9, 8, 7, 6, 5
        )

        val parsed = LinkProtocol.readUdpFrame(ByteArrayInputStream(frame))
        assertNotNull(parsed)
        assertEquals("1.2.3.4", parsed!!.address.host)
        assertEquals(8080, parsed.address.port)
        assertArrayEquals(byteArrayOf(9, 8, 7, 6, 5), parsed.payload)
    }

    @Test
    fun `domain addresses survive both directions`() {
        val output = ByteArrayOutputStream()
        output.write(LinkProtocol.ATYP_DOMAIN)
        output.write(11)
        output.write("example.com".toByteArray())
        output.write(443 shr 8)
        output.write(443 and 0xFF)

        val parsed = LinkProtocol.readAddress(ByteArrayInputStream(output.toByteArray()))
        assertEquals("example.com", parsed.host)
        assertEquals(443, parsed.port)
        assertNull(parsed.address)
    }

    @Test
    fun `token normalization accepts grouped display form`() {
        assertEquals("ABCD2345EFGH6789", LinkProtocol.normalizeToken(" abcd-2345 efgh-6789 "))
        assertTrue(LinkProtocol.normalizeToken("ABCD2345EFGH6789").length == 16)
    }

    @Test
    fun `readUdpFrame returns null on clean eof`() {
        assertEquals(null, LinkProtocol.readUdpFrame(ByteArrayInputStream(ByteArray(0))))
    }
}
