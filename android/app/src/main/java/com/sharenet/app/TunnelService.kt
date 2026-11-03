package com.sharenet.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.os.SystemClock
import java.time.LocalTime
import java.time.format.DateTimeFormatter
import java.util.concurrent.ConcurrentLinkedDeque

class TunnelService : Service() {

    companion object {
        const val ACTION_STOP = "com.sharenet.app.action.STOP"
        const val EXTRA_USB = "usb"
        const val EXTRA_WIFI = "wifi"
        const val EXTRA_TOKEN = "token"
        const val WIFI_PORT = 1080

        private const val CHANNEL_ID = "sharenet_tunnel"
        private const val NOTIFICATION_ID = 1001

        @Volatile
        var running = false
            private set

        @Volatile
        var usbListening = false
            private set

        @Volatile
        var wifiListening = false
            private set

        @Volatile
        var connections = 0
            private set

        @Volatile
        var lastError: String? = null
            private set

        @Volatile
        var startedAt = 0L
            private set

        private val logs = ConcurrentLinkedDeque<String>()
        private val timeFormat = DateTimeFormatter.ofPattern("HH:mm:ss")

        fun logsSnapshot(): String {
            if (logs.isEmpty()) return "No activity yet."
            return logs.toList().takeLast(8).joinToString("\n")
        }

        fun addLog(line: String) {
            val stamp = LocalTime.now().format(timeFormat)
            logs.addLast("$stamp  $line")
            while (logs.size > 100) {
                logs.pollFirst()
            }
        }
    }

    private var server: LinkServer? = null
    private var wakeLock: PowerManager.WakeLock? = null

    /** PdaNet keeps a Wi-Fi lock in Wi-Fi mode so the radio never naps mid-transfer. */
    private var wifiLock: WifiManager.WifiLock? = null

    private var samplerThread: Thread? = null

    @Volatile
    private var sampling = false
    private var lastNotificationAt = 0L

    private val notificationManager: NotificationManager
        get() = getSystemService(NotificationManager::class.java)

    private val linkListener = object : LinkServer.Listener {
        override fun onStateChanged() {
            val current = server ?: return
            usbListening = current.usbListening
            wifiListening = current.wifiListening
            connections = current.activeConnections
            lastError = current.lastError
            updateNotification()
        }

        override fun onLog(line: String) {
            addLog(line)
            updateNotification()
        }
    }

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopEverything()
            return START_NOT_STICKY
        }

        val usb = intent?.getBooleanExtra(EXTRA_USB, Prefs.usbEnabled(this))
            ?: Prefs.usbEnabled(this)
        val wifi = intent?.getBooleanExtra(EXTRA_WIFI, Prefs.wifiEnabled(this))
            ?: Prefs.wifiEnabled(this)
        val token = intent?.getStringExtra(EXTRA_TOKEN) ?: Prefs.ensureToken(this)

        if (!usb && !wifi) {
            addLog("No transport selected")
            stopSelf()
            return START_NOT_STICKY
        }

        createNotificationChannel()
        running = true
        startedAt = System.currentTimeMillis()
        lastError = null
        startForeground(NOTIFICATION_ID, buildNotification("Starting…"))
        acquireWakeLock()
        if (wifi) acquireWifiLock() else releaseWifiLock()

        // Wi-Fi mode opens the ShareNet access point so the PC joins by itself
        // instead of being told an address.
        if (wifi) HotspotController.start(this, token) else HotspotController.stop()

        if (samplerThread == null) {
            TunnelStats.startSession()
            startSampler()
        }

        server?.stop()
        val newServer = LinkServer(token, { NetInfo.dnsServers(this) }, linkListener)
        server = newServer
        addLog("Starting ${transportLabel(usb, wifi)}")
        newServer.start(usb, wifi, WIFI_PORT)
        updateNotification()
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        stopSampler()
        TunnelStats.stopSession()
        server?.stop()
        server = null
        running = false
        usbListening = false
        wifiListening = false
        connections = 0
        releaseWifiLock()
        releaseWakeLock()
        HotspotController.stop()
        stopForeground(STOP_FOREGROUND_REMOVE)
        addLog("Stopped")
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun stopEverything() {
        addLog("Stopping")
        server?.stop()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun transportLabel(usb: Boolean, wifi: Boolean): String = when {
        usb && wifi -> "USB + Wi-Fi"
        usb -> "USB"
        wifi -> "Wi-Fi"
        else -> "no transport"
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= 26) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "ShareNet tunnel",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Shown while ShareNet shares this phone's connection"
                setShowBadge(false)
            }
            notificationManager.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(text: String): Notification {
        val openIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val stopIntent = PendingIntent.getService(
            this,
            1,
            Intent(this, TunnelService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val builder = Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_stat_sharenet)
            .setContentTitle("ShareNet is sharing your connection")
            .setContentText(text)
            .setContentIntent(openIntent)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setCategory(Notification.CATEGORY_SERVICE)
            .addAction(
                android.R.drawable.ic_menu_close_clear_cancel,
                "Stop",
                stopIntent
            )

        if (Build.VERSION.SDK_INT >= 31) {
            builder.setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE)
        }
        return builder.build()
    }

    private fun updateNotification() {
        if (!running) return
        val text = lastError ?: run {
            val transports = buildList {
                if (usbListening) add("USB")
                if (wifiListening) add("Wi-Fi")
            }.joinToString(" + ").ifEmpty { "Starting" }
            val stats = TunnelStats.snapshot()
            val (downValue, downUnit) = Format.rate(stats.downloadRate)
            val (upValue, upUnit) = Format.rate(stats.uploadRate)
            val suffix = if (connections == 1) "1 connection" else "$connections connections"
            "$transports · ↓ $downValue$downUnit ↑ $upValue$upUnit · $suffix"
        }
        try {
            notificationManager.notify(NOTIFICATION_ID, buildNotification(text))
        } catch (_: Exception) {
            // The notification is best effort.
        }
    }

    private fun startSampler() {
        sampling = true
        lastNotificationAt = SystemClock.elapsedRealtime()
        samplerThread = Thread({
            while (sampling) {
                TunnelStats.sample()
                val now = SystemClock.elapsedRealtime()
                if (now - lastNotificationAt >= 2_000L) {
                    lastNotificationAt = now
                    updateNotification()
                }
                try {
                    Thread.sleep(500L)
                } catch (_: InterruptedException) {
                    break
                }
            }
        }, "sharenet-stats").apply {
            isDaemon = true
            start()
        }
    }

    private fun stopSampler() {
        sampling = false
        samplerThread?.interrupt()
        samplerThread = null
    }

    private fun acquireWakeLock() {
        if (wakeLock?.isHeld == true) return
        val powerManager = getSystemService(PowerManager::class.java)
        val lock = powerManager.newWakeLock(
            PowerManager.PARTIAL_WAKE_LOCK,
            "ShareNet::Tunnel"
        )
        lock.setReferenceCounted(false)
        // No timeout: the relay must keep running until the user stops it.
        lock.acquire()
        wakeLock = lock
    }

    private fun acquireWifiLock() {
        if (wifiLock?.isHeld == true) return
        val manager = applicationContext.getSystemService(WifiManager::class.java) ?: return
        @Suppress("DEPRECATION")
        val lock = manager.createWifiLock(
            WifiManager.WIFI_MODE_FULL_HIGH_PERF,
            "ShareNet::Wifi"
        )
        lock.setReferenceCounted(false)
        lock.acquire()
        wifiLock = lock
    }

    private fun releaseWifiLock() {
        try {
            wifiLock?.release()
        } catch (_: Exception) {
        }
        wifiLock = null
    }

    private fun releaseWakeLock() {
        try {
            wakeLock?.release()
        } catch (_: Exception) {
        }
        wakeLock = null
    }
}
