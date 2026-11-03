package com.sharenet.app

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import java.net.Inet4Address
import java.net.InetAddress
import java.net.NetworkInterface
import java.util.Collections
import java.util.Locale

object NetInfo {
    private const val DNS_CACHE_MS = 10_000L

    @Volatile
    private var dnsCache: List<InetAddress> = emptyList()

    @Volatile
    private var dnsCachedAt = 0L

    fun vpnActive(context: Context): Boolean {
        val manager = context.getSystemService(ConnectivityManager::class.java) ?: return false
        val active = manager.activeNetwork ?: return false
        return manager.getNetworkCapabilities(active)
            ?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
    }

    fun wifiIp(context: Context): String? {
        val manager = context.getSystemService(ConnectivityManager::class.java)
        if (manager != null) {
            val active = manager.activeNetwork
            val capabilities = active?.let { manager.getNetworkCapabilities(it) }
            if (capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true) {
                val address = manager.getLinkProperties(active)?.linkAddresses
                    ?.map { it.address }
                    ?.firstOrNull {
                        it is Inet4Address && !it.isLoopbackAddress && !it.isLinkLocalAddress
                    }
                if (address != null) return address.hostAddress
            }
        }

        // Hotspots and secondary Wi-Fi interfaces are not always the active network.
        try {
            val interfaces = NetworkInterface.getNetworkInterfaces() ?: return null
            for (nif in Collections.list(interfaces)) {
                if (!nif.isUp || nif.isLoopback) continue
                val name = nif.name.lowercase(Locale.US)
                val looksLikeWifi = name.startsWith("wlan") ||
                    name.startsWith("ap") ||
                    name.startsWith("swlan") ||
                    name.startsWith("softap")
                if (!looksLikeWifi) continue

                for (address in Collections.list(nif.inetAddresses)) {
                    if (address is Inet4Address &&
                        !address.isLoopbackAddress &&
                        !address.isLinkLocalAddress &&
                        address.isSiteLocalAddress
                    ) {
                        return address.hostAddress
                    }
                }
            }
        } catch (_: Exception) {
            // Best effort only.
        }
        return null
    }

    /**
     * DNS servers that this phone itself would use. When a VPN is active its
     * DNS servers come first, so DNS forwarded by the PC follows the VPN's
     * policy instead of depending on public resolvers being reachable.
     */
    @Suppress("DEPRECATION")
    fun dnsServers(context: Context): List<InetAddress> {
        val now = System.currentTimeMillis()
        val cached = dnsCache
        if (cached.isNotEmpty() && now - dnsCachedAt < DNS_CACHE_MS) {
            return cached
        }

        val result = queryDnsServers(context)
        dnsCache = result
        dnsCachedAt = now
        return result
    }

    @Suppress("DEPRECATION")
    private fun queryDnsServers(context: Context): List<InetAddress> {
        val fallback = listOfNotNull(
            runCatching { InetAddress.getByName("1.1.1.1") }.getOrNull(),
            runCatching { InetAddress.getByName("8.8.8.8") }.getOrNull()
        )

        val manager = context.getSystemService(ConnectivityManager::class.java) ?: return fallback
        try {
            val networks = manager.allNetworks.toList()
            val vpn = networks.firstOrNull { network ->
                manager.getNetworkCapabilities(network)
                    ?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
            }

            val ordered = buildList {
                // The active network is app-specific: it is the VPN network when
                // this app is included in the VPN, or the underlying network when
                // it is not. Prefer whatever this app actually uses.
                manager.activeNetwork?.let { add(it) }
                if (vpn != null) add(vpn)
                addAll(networks)
            }.distinct()

            for (network in ordered) {
                val addresses = manager.getLinkProperties(network)?.dnsServers.orEmpty()
                    .filter { !it.isAnyLocalAddress && !it.isLoopbackAddress }
                if (addresses.isNotEmpty()) return addresses
            }
        } catch (_: Exception) {
            // Fall back to public resolvers.
        }

        return fallback
    }
}
