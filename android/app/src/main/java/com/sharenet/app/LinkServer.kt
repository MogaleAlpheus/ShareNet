package com.sharenet.app

import android.net.LocalServerSocket
import android.net.LocalSocket
import android.net.LocalSocketAddress
import com.sharenet.app.LinkProtocol.readAddress
import com.sharenet.app.LinkProtocol.readFully
import com.sharenet.app.LinkProtocol.readU16
import com.sharenet.app.LinkProtocol.readU8
import com.sharenet.app.LinkProtocol.readUdpFrame
import com.sharenet.app.LinkProtocol.writeU8
import com.sharenet.app.LinkProtocol.writeUdpFrame
import java.io.Closeable
import java.io.EOFException
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.lang.reflect.Method
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.net.SocketTimeoutException
import java.security.MessageDigest
import java.util.Collections
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

interface Duplex : Closeable {
    val input: InputStream
    val output: OutputStream
    val label: String
    fun setReadTimeout(milliseconds: Int)
    fun shutdownOutput()
}

sealed class SocketDuplex : Duplex {
    class Tcp(private val socket: Socket) : SocketDuplex() {
        override val input: InputStream = socket.getInputStream()
        override val output: OutputStream = socket.getOutputStream()
        override val label: String = "tcp:${socket.inetAddress?.hostAddress}:${socket.port}"

        override fun setReadTimeout(milliseconds: Int) {
            socket.soTimeout = milliseconds
        }

        override fun shutdownOutput() {
            try {
                socket.shutdownOutput()
            } catch (_: Exception) {
            }
        }

        override fun close() {
            try {
                socket.close()
            } catch (_: Exception) {
            }
        }
    }

    class Local(private val socket: LocalSocket) : SocketDuplex() {
        override val input: InputStream = socket.inputStream
        override val output: OutputStream = socket.outputStream
        // LocalSocket.getRemoteSocketAddress() is an unimplemented stub in
        // AOSP and always throws UnsupportedOperationException, so the label
        // must not touch it.
        override val label: String = "usb"

        init {
            try {
                // LocalSocket buffer sizes default to the system's Unix domain
                // socket limits (~208 KB). Raise them to match the TCP sockets
                // so the relay isn't throttled by a tiny window.
                socket.receiveBufferSize = 1 shl 20
                socket.sendBufferSize = 1 shl 20
            } catch (_: Exception) {
                // Use the platform defaults when a size is rejected.
            }
        }

        override fun setReadTimeout(milliseconds: Int) {
            socket.soTimeout = milliseconds
        }

        override fun shutdownOutput() {
            try {
                socket.shutdownOutput()
            } catch (_: Exception) {
            }
        }

        override fun close() {
            try {
                socket.close()
            } catch (_: Exception) {
            }
        }
    }
}

class LinkServer(
    private val token: String,
    private val dnsServersProvider: () -> List<InetAddress>,
    private val listener: Listener
) {
    interface Listener {
        fun onStateChanged()
        fun onLog(line: String)
    }

    companion object {
        /**
         * Loopback-only TCP listener used when adb cannot forward to the
         * abstract socket. The PC's fallback is `adb forward tcp:X tcp:30001`.
         */
        const val USB_TCP_FALLBACK_PORT = 30001

        private const val RETRY_MAX_MS = 5_000L
    }

    private val sessions = Collections.synchronizedSet(mutableSetOf<LinkSession>())

    @Volatile
    private var usbServer: LocalServerSocket? = null

    @Volatile
    private var usbFallbackServer: ServerSocket? = null

    @Volatile
    private var wifiServer: ServerSocket? = null

    @Volatile
    private var usbThread: Thread? = null

    @Volatile
    private var usbFallbackThread: Thread? = null

    @Volatile
    private var wifiThread: Thread? = null

    @Volatile
    private var running = false

    /** Identifies the current start/stop cycle so stale supervisors cannot restart. */
    @Volatile
    private var runToken: Any? = null

    @Volatile
    private var usbAbstractListening = false

    @Volatile
    private var usbFallbackListening = false

    @Volatile
    private var wifiListeningFlag = false

    @Volatile
    private var usbError: String? = null

    @Volatile
    private var wifiError: String? = null

    @Volatile
    var lastError: String? = null
        private set

    val usbListening: Boolean
        get() = usbAbstractListening || usbFallbackListening

    val wifiListening: Boolean
        get() = wifiListeningFlag

    val activeConnections: Int
        get() = sessions.size

    fun start(usb: Boolean, wifi: Boolean, wifiPort: Int) {
        stop()
        val token = Any()
        runToken = token
        running = true
        usbError = null
        wifiError = null
        publishError()
        if (usb) {
            startUsbAbstract(token)
            // Keep a loopback TCP path as a fallback for devices where the
            // abstract socket cannot be forwarded. It is never exposed off
            // the phone; adb reaches it from the device side.
            startUsbFallback(token)
        }
        if (wifi) startWifi(wifiPort, token)
        listener.onStateChanged()
    }

    fun stop() {
        running = false
        runToken = null

        val currentUsb = usbServer
        usbServer = null
        closeQuietly(currentUsb)

        val currentFallback = usbFallbackServer
        usbFallbackServer = null
        closeQuietly(currentFallback)

        val currentWifi = wifiServer
        wifiServer = null
        closeQuietly(currentWifi)

        usbAbstractListening = false
        usbFallbackListening = false
        wifiListeningFlag = false

        // Wait for the accept loops to notice the closed sockets. Without this,
        // a fast stop/start can still find the local socket name in use. The
        // run token keeps a supervisor that is sleeping in a retry delay from
        // rebinding after stop() (or into the next session).
        val usb = usbThread
        val fallback = usbFallbackThread
        val wifi = wifiThread
        joinListener(usb, 1_500L)
        joinListener(fallback, 1_500L)
        joinListener(wifi, 1_500L)
        // Closing a LocalServerSocket does not reliably wake a thread parked in
        // accept(). While that thread is parked the abstract name stays bound,
        // so the next start fails with "Address already in use". A throwaway
        // connection releases the listener.
        if (usb != null && usb.isAlive) {
            pokeUsbListener()
            joinListener(usb, 1_500L)
        }
        usbThread = null
        usbFallbackThread = null
        wifiThread = null

        val snapshot = synchronized(sessions) { sessions.toList() }
        snapshot.forEach { it.close() }
        sessions.clear()

        usbError = null
        wifiError = null
        publishError()
        listener.onStateChanged()
    }

    private fun joinListener(thread: Thread?, timeoutMs: Long) {
        if (thread == null || thread === Thread.currentThread()) return
        try {
            thread.join(timeoutMs)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
    }

    private fun closeQuietly(closeable: Closeable?) {
        try {
            closeable?.close()
        } catch (_: Exception) {
        }
    }

    /** Connects once and disconnects, unblocking an accept() that is parked. */
    private fun pokeUsbListener() {
        val client = LocalSocket()
        try {
            client.connect(LocalSocketAddress("sharenet"))
        } catch (_: Exception) {
        } finally {
            closeQuietly(client)
        }
    }

    private fun describe(error: Throwable): String {
        val message = error.message?.trim()
        return if (message.isNullOrEmpty()) {
            error.javaClass.simpleName
        } else {
            "${error.javaClass.simpleName}: $message"
        }
    }

    /** Only report an error when no transport has a working listener. */
    private fun publishError() {
        lastError = when {
            usbListening || wifiListeningFlag -> null
            usbError != null -> usbError
            wifiError != null -> wifiError
            else -> null
        }
    }

    private fun retryDelay(attempt: Int): Long =
        (500L * attempt).coerceAtMost(RETRY_MAX_MS)

    private fun sleepBeforeRetry(milliseconds: Long): Boolean {
        try {
            Thread.sleep(milliseconds)
            return true
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
            return false
        }
    }

    private fun startUsbAbstract(token: Any) {
        val thread = Thread({ superviseUsbAbstract(token) }, "sharenet-usb-listener")
        usbThread = thread
        thread.start()
    }

    private fun superviseUsbAbstract(token: Any) {
        var attempt = 0
        while (running && runToken === token) {
            try {
                runUsbAbstract(token)
                return
            } catch (e: Exception) {
                if (!running || runToken !== token) return
                attempt++
                usbError = "USB listener failed: ${describe(e)}"
                publishError()
                val delay = retryDelay(attempt)
                listener.onLog("USB listener failed: ${describe(e)} — retrying in ${delay}ms")
                listener.onStateChanged()
                if (!sleepBeforeRetry(delay)) return
            }
        }
    }

    private fun runUsbAbstract(token: Any) {
        var server: LocalServerSocket? = null
        try {
            var lastFailure: Exception? = null
            // Give a previous listener a few seconds to release the name when
            // the user stops and starts sharing quickly. If it still fails,
            // the supervisor above retries with a longer backoff.
            for (attempt in 0 until 24) {
                if (!running || runToken !== token) return
                try {
                    server = LocalServerSocket("sharenet")
                    break
                } catch (e: Exception) {
                    lastFailure = e
                    // A previous listener may still be parked in accept()
                    // after its socket was closed; wake it so it can exit
                    // and release the name.
                    pokeUsbListener()
                    if (!sleepBeforeRetry(250L)) return
                }
            }
            val bound = server ?: throw IOException(
                lastFailure?.let { describe(it) } ?: "unable to bind USB socket"
            )

            usbServer = bound
            usbAbstractListening = true
            usbError = null
            publishError()
            listener.onLog("USB listener ready (localabstract:sharenet)")
            listener.onStateChanged()

            while (running && runToken === token && usbServer === bound) {
                val socket = try {
                    bound.accept()
                } catch (e: Exception) {
                    if (!running || runToken !== token || usbServer !== bound) return
                    throw IOException("accept failed: ${describe(e)}")
                }
                if (!running || runToken !== token || usbServer !== bound) {
                    // Stopped while parked in accept(): drop the connection
                    // instead of starting a session on a dead listener.
                    closeQuietly(socket)
                    return
                }
                try {
                    startSession(SocketDuplex.Local(socket), requireToken = false)
                } catch (e: Exception) {
                    // One bad session must never take the listener down with it.
                    android.util.Log.e("ShareNet", "USB session setup failed", e)
                    listener.onLog("USB session setup failed: ${describe(e)}")
                    closeQuietly(socket)
                }
            }
        } finally {
            // The accept loop can also exit because the server was stopped,
            // not only because stop() closed the socket. Always release the
            // local socket here, otherwise the next start fails with
            // "Address already in use".
            closeQuietly(server)
            if (usbServer === server) usbServer = null
            usbAbstractListening = false
            publishError()
            listener.onStateChanged()
        }
    }

    private fun startUsbFallback(token: Any) {
        val thread = Thread({ superviseUsbFallback(token) }, "sharenet-usb-fallback")
        usbFallbackThread = thread
        thread.start()
    }

    private fun superviseUsbFallback(token: Any) {
        var attempt = 0
        while (running && runToken === token) {
            try {
                runUsbFallback(token)
                return
            } catch (e: Exception) {
                if (!running || runToken !== token) return
                attempt++
                val delay = (5_000L * attempt).coerceAtMost(30_000L)
                // The abstract listener may still be healthy, so this is only
                // a warning, never the primary error state. Log the first few
                // attempts, then retry quietly so a busy port cannot flood the
                // activity log.
                if (attempt <= 3) {
                    listener.onLog(
                        "USB TCP fallback unavailable: ${describe(e)} — retrying in ${delay}ms"
                    )
                }
                if (!sleepBeforeRetry(delay)) return
            }
        }
    }

    private fun runUsbFallback(token: Any) {
        val socket = ServerSocket()
        try {
            socket.reuseAddress = true
            socket.bind(
                InetSocketAddress(
                    InetAddress.getByName("127.0.0.1"),
                    USB_TCP_FALLBACK_PORT
                )
            )
            usbFallbackServer = socket
            usbFallbackListening = true
            publishError()
            listener.onLog("USB TCP fallback ready on 127.0.0.1:$USB_TCP_FALLBACK_PORT")
            listener.onStateChanged()

            while (running && runToken === token && usbFallbackServer === socket) {
                val client = try {
                    socket.accept()
                } catch (e: Exception) {
                    if (!running || runToken !== token || usbFallbackServer !== socket) return
                    throw IOException("accept failed: ${describe(e)}")
                }
                if (!running || runToken !== token || usbFallbackServer !== socket) {
                    closeQuietly(client)
                    return
                }
                try {
                    startSession(SocketDuplex.Tcp(client), requireToken = false)
                } catch (e: Exception) {
                    android.util.Log.e("ShareNet", "USB fallback session setup failed", e)
                    listener.onLog("USB fallback session setup failed: ${describe(e)}")
                    closeQuietly(client)
                }
            }
        } finally {
            closeQuietly(socket)
            if (usbFallbackServer === socket) usbFallbackServer = null
            usbFallbackListening = false
            publishError()
            listener.onStateChanged()
        }
    }

    private fun startWifi(port: Int, token: Any) {
        val thread = Thread({ superviseWifi(port, token) }, "sharenet-wifi-listener")
        wifiThread = thread
        thread.start()
    }

    private fun superviseWifi(port: Int, token: Any) {
        var attempt = 0
        while (running && runToken === token) {
            try {
                runWifi(port, token)
                return
            } catch (e: Exception) {
                if (!running || runToken !== token) return
                attempt++
                wifiError = "Wi-Fi listener failed: ${describe(e)}"
                publishError()
                val delay = retryDelay(attempt)
                listener.onLog("Wi-Fi listener failed: ${describe(e)} — retrying in ${delay}ms")
                listener.onStateChanged()
                if (!sleepBeforeRetry(delay)) return
            }
        }
    }

    private fun runWifi(port: Int, token: Any) {
        val socket = ServerSocket()
        try {
            socket.reuseAddress = true
            // Set receive buffer on the listening socket so accepted connections inherit it.
            try {
                socket.setReceiveBufferSize(1 shl 20)
            } catch (_: Exception) {
            }
            socket.bind(InetSocketAddress(port))
            wifiServer = socket
            wifiListeningFlag = true
            wifiError = null
            publishError()
            listener.onLog("Wi-Fi listener ready on port $port")
            // Let the PC find this phone instead of asking the user to read the
            // address off the screen (PdaNet's model: the client discovers us).
            WifiAnnouncer.start(port)
            listener.onStateChanged()

            while (running && runToken === token && wifiServer === socket) {
                val client = try {
                    socket.accept()
                } catch (e: Exception) {
                    if (!running || runToken !== token || wifiServer !== socket) return
                    throw IOException("accept failed: ${describe(e)}")
                }
                if (!running || runToken !== token || wifiServer !== socket) {
                    closeQuietly(client)
                    return
                }
                // The listening socket only carries its receive window over to
                // accepted sockets; the send buffer stays at the platform
                // default, which caps uploads on a fast link. Size both here.
                tuneLinkSocket(client)
                try {
                    startSession(SocketDuplex.Tcp(client), requireToken = true)
                } catch (e: Exception) {
                    android.util.Log.e("ShareNet", "Wi-Fi session setup failed", e)
                    listener.onLog("Wi-Fi session setup failed: ${describe(e)}")
                    closeQuietly(client)
                }
            }
        } finally {
            WifiAnnouncer.stop()
            closeQuietly(socket)
            if (wifiServer === socket) wifiServer = null
            wifiListeningFlag = false
            publishError()
            listener.onStateChanged()
        }
    }

    /** 1 MB windows in both directions, throughput-first congestion choice. */
    private fun tuneLinkSocket(socket: Socket) {
        try {
            socket.setPerformancePreferences(0, 2, 1)
            socket.receiveBufferSize = 1 shl 20
            socket.sendBufferSize = 1 shl 20
        } catch (_: Exception) {
            // Keep the platform defaults when a size is rejected.
        }
    }

    private fun startSession(duplex: Duplex, requireToken: Boolean) {
        val session = LinkSession(duplex, token, requireToken, dnsServersProvider, object : LinkSession.Listener {
            override fun onClosed(session: LinkSession) {
                sessions.remove(session)
                listener.onStateChanged()
            }

            override fun onLog(line: String) {
                listener.onLog(line)
            }
        })
        sessions.add(session)
        listener.onStateChanged()
        listener.onLog("New ${if (requireToken) "Wi-Fi" else "USB"} session (${sessions.size} active)")
        session.start()
    }
}

class LinkSession(
    private val link: Duplex,
    private val token: String,
    private val requireToken: Boolean,
    private val dnsServersProvider: () -> List<InetAddress>,
    private val listener: Listener
) : Thread("sharenet-link-session") {

    interface Listener {
        fun onClosed(session: LinkSession)
        fun onLog(line: String)
    }

    @Volatile
    private var closed = false
    private val closeOnce = AtomicBoolean(false)

    // DNS replies carry the query's transaction ID. When a PC query to a public
    // resolver is redirected to the phone's VPN resolver, the reply's source
    // address is rewritten back to the address the PC expects.
    private val dnsReplies = ConcurrentHashMap<Int, InetSocketAddress>()
    private val dnsRotation = AtomicInteger()

    private fun chooseDnsServer(): InetAddress? {
        val servers = dnsServersProvider()
        if (servers.isEmpty()) return null
        val index = (dnsRotation.getAndIncrement() and Int.MAX_VALUE) % servers.size
        return servers[index]
    }

    private fun dnsTransactionId(buffer: ByteArray, offset: Int, length: Int): Int? {
        if (length < 12 || offset + 2 > buffer.size) return null
        return ((buffer[offset].toInt() and 0xFF) shl 8) or
            (buffer[offset + 1].toInt() and 0xFF)
    }

    override fun run() {
        try {
            link.setReadTimeout(10_000)

            val magic = LinkProtocol.readInt(link.input)
            if (magic != LinkProtocol.MAGIC) {
                listener.onLog("Rejected connection: bad magic")
                return
            }

            val version = link.input.readU8()
            if (version != LinkProtocol.VERSION) {
                link.output.writeU8(LinkProtocol.STATUS_BAD_VERSION)
                link.output.flush()
                listener.onLog("Rejected connection: unsupported protocol version $version")
                return
            }

            val authMode = link.input.readU8()
            when {
                authMode == LinkProtocol.AUTH_TOKEN -> {
                    val length = link.input.readU8()
                    if (length <= 0 || length > 128) {
                        link.output.writeU8(LinkProtocol.STATUS_BAD_AUTH)
                        link.output.flush()
                        return
                    }
                    val received = ByteArray(length)
                    link.input.readFully(received)
                    val supplied = LinkProtocol.normalizeToken(String(received, Charsets.UTF_8))
                    val expected = LinkProtocol.normalizeToken(token)
                    if (!MessageDigest.isEqual(
                            supplied.toByteArray(Charsets.UTF_8),
                            expected.toByteArray(Charsets.UTF_8)
                        )
                    ) {
                        link.output.writeU8(LinkProtocol.STATUS_BAD_AUTH)
                        link.output.flush()
                        listener.onLog("Rejected Wi-Fi client: wrong password")
                        return
                    }
                }

                authMode == LinkProtocol.AUTH_NONE -> {
                    if (requireToken) {
                        link.output.writeU8(LinkProtocol.STATUS_BAD_AUTH)
                        link.output.flush()
                        listener.onLog("Rejected Wi-Fi client: password required")
                        return
                    }
                }

                else -> {
                    link.output.writeU8(LinkProtocol.STATUS_BAD_AUTH)
                    link.output.flush()
                    return
                }
            }

            link.output.writeU8(LinkProtocol.STATUS_OK)
            link.output.flush()
            link.setReadTimeout(0)

            when (val mode = link.input.readU8()) {
                LinkProtocol.MODE_TCP -> handleTcp()
                LinkProtocol.MODE_UDP -> handleUdp()
                else -> listener.onLog("Rejected connection: unknown mode $mode")
            }
        } catch (_: EOFException) {
            // Normal disconnect.
        } catch (_: SocketTimeoutException) {
            listener.onLog("Connection timed out during handshake")
        } catch (e: Exception) {
            android.util.Log.e("ShareNet", "Session error", e)
            if (!closed) {
                listener.onLog("Session error: ${e.javaClass.simpleName}: ${e.message}")
            }
        } finally {
            close()
        }
    }

    private fun handleTcp() {
        val target = LinkProtocol.readAddress(link.input)
        val resolved = try {
            target.resolve()
        } catch (e: Exception) {
            safeStatus(LinkProtocol.STATUS_CONNECT_FAILED)
            listener.onLog("DNS lookup failed for ${target.host}")
            return
        }

        val socket = Socket()
        try {
            socket.tcpNoDelay = true
            // Enable TCP window scaling (RFC 1323) for high-BDP paths.
            // Available since API 24 (Android 7.0). Use try-catch for compatibility.
            try {
                socket.setPerformancePreferences(0, 2, 1) // prioritize throughput
                // tcpWindowScaling property added in API 24; use reflection for compatibility
                val method = socket.javaClass.getMethod("setTcpWindowScaling", Boolean::class.java)
                method.invoke(socket, true)
            } catch (_: Exception) {
            }
            try {
                // The whole PC shares this link; bigger socket buffers keep
                // high-latency, high-throughput streams moving.
                socket.receiveBufferSize = 1 shl 20
                socket.sendBufferSize = 1 shl 20
            } catch (_: Exception) {
                // Keep the platform defaults when a size is rejected.
            }
            val dnsServer = if (target.port == 53) chooseDnsServer() else null
            socket.connect(InetSocketAddress(dnsServer ?: resolved, target.port), 15_000)
            safeStatus(LinkProtocol.STATUS_OK)
            listener.onLog("TCP ${target.host}:${target.port}")
            relayTcp(socket)
        } catch (e: Exception) {
            safeStatus(LinkProtocol.STATUS_CONNECT_FAILED)
            listener.onLog("TCP connect failed ${target.host}:${target.port}: ${e.message}")
        } finally {
            try {
                socket.close()
            } catch (_: Exception) {
            }
        }
    }

    private fun relayTcp(target: Socket) {
        val up = Thread({
            try {
                copy(link.input, target.getOutputStream()) { TunnelStats.addUp(it) }
                try {
                    target.shutdownOutput()
                } catch (_: Exception) {
                }
            } catch (_: Exception) {
            }
        }, "sharenet-tcp-up")

        val down = Thread({
            try {
                copy(target.getInputStream(), link.output) { TunnelStats.addDown(it) }
                try {
                    link.shutdownOutput()
                } catch (_: Exception) {
                }
            } catch (_: Exception) {
            }
        }, "sharenet-tcp-down")

        up.start()
        down.start()
        up.join()
        down.join(30_000L)
        if (down.isAlive) {
            try {
                target.close()
            } catch (_: Exception) {
            }
        }
        down.join(5_000L)
    }

    private fun copy(input: InputStream, output: OutputStream, onBytes: (Int) -> Unit) {
        // 128 KB reads keep the relay from being the bottleneck on fast links.
        val buffer = ByteArray(128 * 1024)
        while (!closed) {
            val read = input.read(buffer)
            if (read < 0) return
            if (read == 0) continue
            output.write(buffer, 0, read)
            // Do not flush after every write; let TCP's Nagle algorithm and
            // the socket send buffer batch data naturally. Flushing every
            // 128 KB forces a round-trip and caps throughput at window/RTT.
            if (!closed) onBytes(read)
        }
    }

    private fun handleUdp() {
        val udp = DatagramSocket()
        udp.soTimeout = 30_000
        try {
            udp.receiveBufferSize = 1 shl 20
            udp.sendBufferSize = 1 shl 20
        } catch (_: Exception) {
            // Keep the platform defaults when a size is rejected.
        }
        listener.onLog("UDP association started")

        val reader = Thread({
            val buffer = ByteArray(LinkProtocol.MAX_FRAME)
            try {
                while (!closed) {
                    val packet = DatagramPacket(buffer, buffer.size)
                    try {
                        udp.receive(packet)
                    } catch (_: SocketTimeoutException) {
                        continue
                    }
                    val sourcePort = packet.port
                    val original = if (sourcePort == 53) {
                        dnsTransactionId(packet.data, packet.offset, packet.length)
                            ?.let { dnsReplies.remove(it) }
                    } else {
                        null
                    }
                    TunnelStats.addDown(packet.length)
                    LinkProtocol.writeUdpFrame(
                        link.output,
                        original?.address ?: packet.address,
                        original?.port ?: sourcePort,
                        packet.data,
                        packet.offset,
                        packet.length
                    )
                }
            } catch (e: Exception) {
                if (!closed) listener.onLog("UDP receive ended: ${e.message}")
            } finally {
                try {
                    udp.close()
                } catch (_: Exception) {
                }
                close()
            }
        }, "sharenet-udp-reader")
        reader.isDaemon = true
        reader.start()

        try {
            while (!closed) {
                val frame = LinkProtocol.readUdpFrame(link.input) ?: break
                val target = try {
                    frame.address.resolve()
                } catch (e: Exception) {
                    listener.onLog("UDP DNS lookup failed for ${frame.address.host}")
                    continue
                }
                try {
                    val dnsServer = if (frame.address.port == 53) chooseDnsServer() else null
                    if (dnsServer != null) {
                        val id = dnsTransactionId(frame.payload, 0, frame.payload.size)
                        if (id != null) {
                            if (dnsReplies.size > 512) dnsReplies.clear()
                            dnsReplies[id] = InetSocketAddress(target, frame.address.port)
                        }
                    }
                    udp.send(
                        DatagramPacket(
                            frame.payload,
                            frame.payload.size,
                            dnsServer ?: target,
                            frame.address.port
                        )
                    )
                    TunnelStats.addUp(frame.payload.size)
                } catch (e: Exception) {
                    listener.onLog("UDP send failed: ${e.message}")
                }
            }
        } catch (_: Exception) {
            // Normal disconnect.
        } finally {
            close()
            try {
                udp.close()
            } catch (_: Exception) {
            }
        }
    }

    private fun safeStatus(status: Int) {
        try {
            link.output.writeU8(status)
            link.output.flush()
        } catch (_: Exception) {
        }
    }

    fun close() {
        if (!closeOnce.compareAndSet(false, true)) return
        closed = true
        try {
            link.close()
        } catch (_: Exception) {
        }
        listener.onClosed(this)
    }
}
