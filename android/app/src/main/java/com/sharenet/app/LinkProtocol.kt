package com.sharenet.app

import java.io.ByteArrayInputStream
import java.io.EOFException
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.util.Locale

/**
 * ShareNet link protocol v1.
 *
 * Transport: TCP (USB via adb forward to localabstract:sharenet, or Wi-Fi port 1080).
 *
 * Client -> server handshake:
 *   magic "SNET" (4) | version (1) | auth mode (1) | [token length (1) | token (n)]
 * Server -> client: status (1)
 * Client -> server: mode (1) = 1 for TCP stream, 2 for UDP association.
 *
 * TCP mode: target address (ATYP/ADDR/PORT), server replies with status, then raw bytes flow.
 * UDP mode: length-prefixed frames, each frame is ATYP/ADDR/PORT + payload.
 */
object LinkProtocol {
    const val MAGIC = 0x534E4554 // "SNET"
    const val VERSION = 1

    const val AUTH_NONE = 0
    const val AUTH_TOKEN = 1

    const val STATUS_OK = 0
    const val STATUS_BAD_AUTH = 1
    const val STATUS_BAD_VERSION = 2
    const val STATUS_BAD_REQUEST = 3
    const val STATUS_CONNECT_FAILED = 4

    const val MODE_TCP = 1
    const val MODE_UDP = 2

    const val ATYP_IPV4 = 1
    const val ATYP_DOMAIN = 3
    const val ATYP_IPV6 = 4

    const val MAX_FRAME = 65535

    data class Address(val host: String, val port: Int, val address: InetAddress? = null) {
        fun resolve(): InetAddress = address ?: InetAddress.getByName(host)
    }

    data class UdpFrame(val address: Address, val payload: ByteArray)

    fun readInt(input: InputStream): Int {
        val b = ByteArray(4)
        input.readFully(b)
        return ((b[0].toInt() and 0xFF) shl 24) or
            ((b[1].toInt() and 0xFF) shl 16) or
            ((b[2].toInt() and 0xFF) shl 8) or
            (b[3].toInt() and 0xFF)
    }

    fun InputStream.readU8(): Int {
        val value = read()
        if (value < 0) throw EOFException("unexpected end of stream")
        return value
    }

    fun InputStream.readU16(): Int {
        val hi = readU8()
        val lo = readU8()
        return (hi shl 8) or lo
    }

    fun OutputStream.writeU8(value: Int) {
        write(value and 0xFF)
    }

    fun OutputStream.writeU16(value: Int) {
        write((value ushr 8) and 0xFF)
        write(value and 0xFF)
    }

    fun InputStream.readFully(buffer: ByteArray, length: Int = buffer.size) {
        var offset = 0
        while (offset < length) {
            val read = read(buffer, offset, length - offset)
            if (read < 0) throw EOFException("unexpected end of stream")
            offset += read
        }
    }

    fun readAddress(input: InputStream): Address {
        val atyp = input.readU8()
        return when (atyp) {
            ATYP_IPV4 -> {
                val bytes = ByteArray(4)
                input.readFully(bytes)
                val address = InetAddress.getByAddress(bytes)
                Address(address.hostAddress ?: "0.0.0.0", input.readU16(), address)
            }
            ATYP_IPV6 -> {
                val bytes = ByteArray(16)
                input.readFully(bytes)
                val address = InetAddress.getByAddress(bytes)
                Address(address.hostAddress ?: "::", input.readU16(), address)
            }
            ATYP_DOMAIN -> {
                val length = input.readU8()
                if (length == 0) throw IOException("empty domain name")
                val bytes = ByteArray(length)
                input.readFully(bytes)
                Address(String(bytes, Charsets.UTF_8), input.readU16())
            }
            else -> throw IOException("unsupported address type $atyp")
        }
    }

    fun writeAddress(output: OutputStream, address: InetAddress, port: Int) {
        val bytes = address.address
        when (bytes.size) {
            4 -> {
                output.writeU8(ATYP_IPV4)
                output.write(bytes)
            }
            16 -> {
                output.writeU8(ATYP_IPV6)
                output.write(bytes)
            }
            else -> throw IOException("unsupported address length ${bytes.size}")
        }
        output.writeU16(port)
    }

    fun writeUdpFrame(
        output: OutputStream,
        address: InetAddress,
        port: Int,
        payload: ByteArray,
        offset: Int,
        length: Int
    ) {
        val addressBytes = address.address
        val frameLength = 1 + addressBytes.size + 2 + length
        if (frameLength > MAX_FRAME) throw IOException("UDP frame is too large")
        synchronized(output) {
            output.writeU16(frameLength)
            output.writeU8(if (addressBytes.size == 4) ATYP_IPV4 else ATYP_IPV6)
            output.write(addressBytes)
            output.writeU16(port)
            output.write(payload, offset, length)
            // Do not flush after every UDP frame; let TCP batch frames
            // naturally. The length-prefixed framing allows the receiver to
            // parse frames correctly regardless of write boundaries.
        }
    }

    fun readUdpFrame(input: InputStream): UdpFrame? {
        val frameLength = try {
            input.readU16()
        } catch (_: EOFException) {
            return null
        }
        if (frameLength < 1) throw IOException("invalid UDP frame length $frameLength")
        val frame = ByteArray(frameLength)
        input.readFully(frame)
        val frameInput = ByteArrayInputStream(frame)
        val address = readAddress(frameInput)
        val payload = frameInput.readBytes()
        return UdpFrame(address, payload)
    }

    fun normalizeToken(value: String): String =
        value.trim().replace("-", "").replace(" ", "").uppercase(Locale.US)
}
