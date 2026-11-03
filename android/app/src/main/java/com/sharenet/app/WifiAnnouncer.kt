package com.sharenet.app

import android.os.Build
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.NetworkInterface
import java.util.Collections

/**
 * Presence beacon for the Wi-Fi transport, the way PdaNet's PC client finds its
 * phone instead of making the user read an address off the screen.
 *
 * While the Wi-Fi listener is up the phone broadcasts a datagram every two
 * seconds on [DISCOVERY_PORT]:
 *
 *     SHARENET1|<link port>|<device model>
 *
 * It carries no secret: a discovered address is still useless without the
 * password, exactly like the announcement PdaNet pairs against. The beacon stops
 * with the listener, so a stopped phone is silent on the network.
 */
object WifiAnnouncer {
    const val DISCOVERY_PORT = 1081

    private const val PREFIX = "SHARENET1"
    private const val INTERVAL_MS = 2_000L

    @Volatile
    private var running = false

    private var socket: DatagramSocket? = null
    private var thread: Thread? = null

    fun start(linkPort: Int) {
        if (running) stop()
        running = true

        val model = Build.MODEL?.trim().orEmpty().ifEmpty { "Android phone" }
        val payload = "$PREFIX|$linkPort|$model".toByteArray(Charsets.UTF_8)

        val socket = try {
            DatagramSocket(null).apply {
                reuseAddress = true
                broadcast = true
                bind(InetSocketAddress(0))
            }
        } catch (e: Exception) {
            running = false
            TunnelService.addLog("Discovery beacon unavailable: ${e.message ?: e.javaClass.simpleName}")
            return
        }
        this.socket = socket

        thread = Thread({
            while (running) {
                for (target in broadcastTargets()) {
                    try {
                        socket.send(DatagramPacket(payload, payload.size, target, DISCOVERY_PORT))
                    } catch (_: Exception) {
                        // A dying interface is normal; the next tick retries.
                    }
                }
                try {
                    Thread.sleep(INTERVAL_MS)
                } catch (_: InterruptedException) {
                    break
                }
            }
        }, "sharenet-discovery").apply {
            isDaemon = true
            start()
        }
    }

    fun stop() {
        running = false
        thread?.interrupt()
        thread = null
        try {
            socket?.close()
        } catch (_: Exception) {
        }
        socket = null
    }

    /**
     * Directed broadcasts are more reliable than 255.255.255.255 on Android, so
     * send both: the limited broadcast plus every interface's subnet broadcast.
     */
    private fun broadcastTargets(): List<InetAddress> {
        val targets = LinkedHashSet<InetAddress>()
        try {
            targets.add(InetAddress.getByName("255.255.255.255"))
        } catch (_: Exception) {
        }
        try {
            val interfaces = NetworkInterface.getNetworkInterfaces() ?: return targets.toList()
            for (nif in Collections.list(interfaces)) {
                if (!nif.isUp || nif.isLoopback) continue
                // interfaceAddresses is a List on Android, not an Enumeration.
                for (address in nif.interfaceAddresses.orEmpty()) {
                    if (address.address is Inet4Address) {
                        address.broadcast?.let { targets.add(it) }
                    }
                }
            }
        } catch (_: Exception) {
        }
        return targets.toList()
    }
}
