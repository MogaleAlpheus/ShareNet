package com.sharenet.app

import android.content.Context
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import java.util.Locale
import java.util.concurrent.Executor

/**
 * Opens the ShareNet access point so the PC joins the phone by itself, the way
 * PdaNet's WiFi Direct hotspot works — no address, no network picker, no Find.
 *
 * The network name and key are not secrets of their own: both come from the link
 * password the user already has, so the PC can compute them locally.
 *
 *     SSID       ShareNet-<first four characters of the password>
 *     passphrase the 16-character password itself
 *
 * A local-only hotspot is used on purpose: it gives the PC a private link to this
 * phone without disturbing the phone's own Wi-Fi or mobile data. Android picks
 * the access point's address, and the PC reads it from the gateway of the network
 * it just joined.
 *
 * Choosing the name/passphrase needs Android 11 (SoftApConfiguration). Those
 * classes are reached reflectively because the SDK stub this app compiles
 * against ships only the empty configuration class; on device they are present.
 */
object HotspotController {
    const val SSID_PREFIX = "ShareNet-"

    /** SoftApConfiguration with a caller-chosen SSID/passphrase is Android 11+. */
    private const val MIN_API = Build.VERSION_CODES.R

    @Volatile
    private var reservation: WifiManager.LocalOnlyHotspotReservation? = null

    @Volatile
    var ssid: String? = null
        private set

    val isRunning: Boolean
        get() = reservation != null

    fun start(context: Context, token: String) {
        if (reservation != null) return

        if (Build.VERSION.SDK_INT < MIN_API) {
            log("Automatic hotspot needs Android 11 or newer; the PC can still join this phone's Wi-Fi network")
            return
        }

        val app = context.applicationContext
        val manager = app.getSystemService(WifiManager::class.java)
        if (manager == null) {
            log("No Wi-Fi radio available for the ShareNet hotspot")
            return
        }

        val name = SSID_PREFIX + token.take(4).uppercase(Locale.US)
        val callback = object : WifiManager.LocalOnlyHotspotCallback() {
            override fun onStarted(res: WifiManager.LocalOnlyHotspotReservation) {
                reservation = res
                ssid = name
                log("Hotspot ready: $name — the PC joins it automatically")
            }

            override fun onStopped() {
                reservation = null
                ssid = null
                log("Hotspot stopped")
            }

            override fun onFailed(reason: Int) {
                reservation = null
                ssid = null
                log("Hotspot unavailable ($reason); the PC can join this Wi-Fi network instead")
            }
        }

        log("Starting the ShareNet hotspot ($name)")
        if (startConfigured(manager, name, token, app.mainExecutor, callback)) return

        // Older or locked-down devices: let Android choose the credentials. The PC
        // cannot derive those, so it has to be pointed at the network by hand.
        try {
            manager.startLocalOnlyHotspot(callback, Handler(Looper.getMainLooper()))
            log("Android chose the hotspot name; join it from the PC and press Find")
        } catch (e: SecurityException) {
            log("Hotspot permission denied; the PC can join this Wi-Fi network instead")
        } catch (e: Exception) {
            log("Hotspot unavailable: ${e.message ?: e.javaClass.simpleName}")
        }
    }

    /**
     * startLocalOnlyHotspot(SoftApConfiguration, Executor, callback) with a chosen
     * SSID and passphrase. Returns false when the platform does not offer it.
     */
    private fun startConfigured(
        manager: WifiManager,
        name: String,
        key: String,
        executor: Executor,
        callback: WifiManager.LocalOnlyHotspotCallback
    ): Boolean {
        return try {
            val configClass = Class.forName("android.net.wifi.SoftApConfiguration")
            val builderClass = Class.forName("android.net.wifi.SoftApConfiguration\$Builder")
            val builder = builderClass.getDeclaredConstructor().newInstance()

            builderClass.getMethod("setSsid", String::class.java).invoke(builder, name)
            val wpa2 = configClass.getField("SECURITY_TYPE_WPA2_PSK").getInt(null)
            builderClass
                .getMethod("setPassphrase", String::class.java, Int::class.javaPrimitiveType)
                .invoke(builder, key, wpa2)
            val config = builderClass.getMethod("build").invoke(builder)

            WifiManager::class.java
                .getMethod(
                    "startLocalOnlyHotspot",
                    configClass,
                    Executor::class.java,
                    WifiManager.LocalOnlyHotspotCallback::class.java
                )
                .invoke(manager, config, executor, callback)
            true
        } catch (e: NoSuchMethodException) {
            false
        } catch (e: ClassNotFoundException) {
            false
        } catch (e: Throwable) {
            log("Hotspot settings rejected: ${e.message ?: e.javaClass.simpleName}")
            false
        }
    }

    fun stop() {
        val current = reservation ?: return
        reservation = null
        ssid = null
        try {
            current.close()
        } catch (_: Exception) {
            // Already released by the system.
        }
        log("Hotspot closed")
    }

    private fun log(line: String) = TunnelService.addLog(line)
}
