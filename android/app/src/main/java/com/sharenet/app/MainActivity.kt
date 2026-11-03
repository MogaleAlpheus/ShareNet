package com.sharenet.app

import android.Manifest
import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.view.View
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.core.view.isVisible

class MainActivity : Activity() {

    private lateinit var statusPill: View
    private lateinit var statusDot: View
    private lateinit var statusPillText: TextView
    private lateinit var connectionText: TextView
    private lateinit var heroStatus: TextView
    private lateinit var heroDetail: TextView
    private lateinit var vpnDot: View
    private lateinit var vpnText: TextView
    private lateinit var startStopButton: ConnectButton
    private lateinit var sessionText: TextView
    private lateinit var downRateText: TextView
    private lateinit var downRateUnit: TextView
    private lateinit var downTotalText: TextView
    private lateinit var downSpark: SparklineView
    private lateinit var upRateText: TextView
    private lateinit var upRateUnit: TextView
    private lateinit var upTotalText: TextView
    private lateinit var upSpark: SparklineView
    private lateinit var usbCard: TransportCard
    private lateinit var wifiCard: TransportCard
    private lateinit var wifiDetails: androidx.cardview.widget.CardView
    private lateinit var wifiIpText: TextView
    private lateinit var wifiPortText: TextView
    private lateinit var wifiAddressHint: TextView
    private lateinit var tokenText: TextView
    private lateinit var copyTokenButton: TextView
    private lateinit var batteryRow: LinearLayout
    private lateinit var batteryValue: TextView
    private lateinit var activityHeader: LinearLayout
    private lateinit var activityChevron: ImageView
    private lateinit var activitySummary: TextView
    private lateinit var logText: TextView

    private lateinit var token: String
    private var activityOpen = false
    private var startAfterPermission = false
    private val REQUEST_HOTSPOT = 101

    private val handler = Handler(Looper.getMainLooper())
    private val refresh = object : Runnable {
        override fun run() {
            updateUi()
            handler.postDelayed(this, 1_000L)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        window.statusBarColor = getColor(R.color.bg)
        window.navigationBarColor = getColor(R.color.bg)

        bindViews()

        token = Prefs.ensureToken(this)

        usbCard.setOnClickListener { if (!TunnelService.running) toggleTransport(usb = true) }
        wifiCard.setOnClickListener { if (!TunnelService.running) toggleTransport(usb = false) }
        copyTokenButton.setOnClickListener { copyPassword() }
        batteryRow.setOnClickListener { openBatterySettings() }
        activityHeader.setOnClickListener { toggleActivity() }
        startStopButton.setOnClickListener {
            if (TunnelService.running) stopTunnel() else startTunnel()
        }

        requestNotificationPermission()
        updateUi()
    }

    private fun bindViews() {
        statusPill = findViewById(R.id.statusPill)
        statusDot = findViewById(R.id.statusDot)
        statusPillText = findViewById(R.id.statusPillText)
        connectionText = findViewById(R.id.connectionText)
        heroStatus = findViewById(R.id.heroStatus)
        heroDetail = findViewById(R.id.heroDetail)
        vpnDot = findViewById(R.id.vpnDot)
        vpnText = findViewById(R.id.vpnText)
        startStopButton = findViewById(R.id.startStopButton)
        sessionText = findViewById(R.id.sessionText)
        downRateText = findViewById(R.id.downRateText)
        downRateUnit = findViewById(R.id.downRateUnit)
        downTotalText = findViewById(R.id.downTotalText)
        downSpark = findViewById(R.id.downSpark)
        upRateText = findViewById(R.id.upRateText)
        upRateUnit = findViewById(R.id.upRateUnit)
        upTotalText = findViewById(R.id.upTotalText)
        upSpark = findViewById(R.id.upSpark)
        usbCard = findViewById(R.id.usbCard)
        wifiCard = findViewById(R.id.wifiCard)
        wifiDetails = findViewById(R.id.wifiDetails)
        wifiIpText = findViewById(R.id.wifiIpText)
        wifiPortText = findViewById(R.id.wifiPortText)
        wifiAddressHint = findViewById(R.id.wifiAddressHint)
        tokenText = findViewById(R.id.tokenText)
        copyTokenButton = findViewById(R.id.copyTokenButton)
        batteryRow = findViewById(R.id.batteryRow)
        batteryValue = findViewById(R.id.batteryValue)
        activityHeader = findViewById(R.id.activityHeader)
        activityChevron = findViewById(R.id.activityChevron)
        activitySummary = findViewById(R.id.activitySummary)
        logText = findViewById(R.id.logText)
    }

    override fun onResume() {
        super.onResume()
        handler.removeCallbacks(refresh)
        handler.post(refresh)
    }

    override fun onPause() {
        super.onPause()
        handler.removeCallbacks(refresh)
    }

    private fun toggleTransport(usb: Boolean) {
        if (TunnelService.running) {
            Toast.makeText(this, "Stop sharing to change the connection type", Toast.LENGTH_SHORT).show()
            return
        }

        if (usb) {
            Prefs.setTransports(this, !Prefs.usbEnabled(this), Prefs.wifiEnabled(this))
        } else {
            Prefs.setTransports(this, Prefs.usbEnabled(this), !Prefs.wifiEnabled(this))
        }
        updateUi()
    }

    private fun startTunnel() {
        val usb = Prefs.usbEnabled(this)
        val wifi = Prefs.wifiEnabled(this)
        if (!usb && !wifi) {
            Toast.makeText(this, "Select USB or Wi-Fi first", Toast.LENGTH_SHORT).show()
            return
        }

        // The hotspot is what lets the PC join by itself, so the permission is
        // part of starting Wi-Fi sharing rather than a surprise later.
        if (wifi && !hasHotspotPermission()) {
            startAfterPermission = true
            requestPermissions(arrayOf(hotspotPermission()), REQUEST_HOTSPOT)
            return
        }

        if (wifi && NetInfo.wifiIp(this) == null) {
            Toast.makeText(
                this,
                "Starting the ShareNet hotspot. The PC joins it automatically.",
                Toast.LENGTH_LONG
            ).show()
        }

        val intent = Intent(this, TunnelService::class.java)
            .putExtra(TunnelService.EXTRA_USB, usb)
            .putExtra(TunnelService.EXTRA_WIFI, wifi)
            .putExtra(TunnelService.EXTRA_TOKEN, token)

        if (Build.VERSION.SDK_INT >= 26) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
    }

    private fun stopTunnel() {
        stopService(Intent(this, TunnelService::class.java))
    }

    private fun updateUi() {
        val running = TunnelService.running
        val error = TunnelService.lastError
        val connections = TunnelService.connections
        val transports = buildList {
            if (TunnelService.usbListening) add("USB")
            if (TunnelService.wifiListening) add("Wi-Fi")
        }

        val usbSelected = Prefs.usbEnabled(this)
        val wifiSelected = Prefs.wifiEnabled(this)

        // Update transport cards (Checkable containers)
        usbCard.isChecked = usbSelected
        wifiCard.isChecked = wifiSelected
        usbCard.isEnabled = !running
        wifiCard.isEnabled = !running
        usbCard.alpha = if (running) 0.55f else 1f
        wifiCard.alpha = if (running) 0.55f else 1f

        wifiDetails.isVisible = wifiSelected

        // Status pill & hero
        when {
            running && error != null -> {
                statusPill.setBackgroundResource(R.drawable.bg_status_error)
                statusDot.setBackgroundResource(R.drawable.dot_error)
                statusPillText.text = getString(R.string.status_error)
                heroStatus.text = "Needs attention"
                heroDetail.text = error
            }

            running && transports.isNotEmpty() -> {
                statusPill.setBackgroundResource(R.drawable.bg_status_connected)
                statusDot.setBackgroundResource(R.drawable.dot_connected)
                statusPillText.text = getString(R.string.status_sharing)
                heroStatus.text = "Sharing over ${transports.joinToString(" + ")}"
                heroDetail.text = getString(R.string.hero_detail_sharing)
            }

            running -> {
                statusPill.setBackgroundResource(R.drawable.bg_status_connecting)
                statusDot.setBackgroundResource(R.drawable.dot_connecting)
                statusPillText.text = getString(R.string.status_starting)
                heroStatus.text = "Starting listeners…"
                heroDetail.text = getString(R.string.hero_detail_sharing)
            }

            else -> {
                statusPill.setBackgroundResource(R.drawable.bg_status_idle)
                statusDot.setBackgroundResource(R.drawable.dot_idle)
                statusPillText.text = getString(R.string.status_idle)
                heroStatus.text = getString(R.string.ready_to_share)
                heroDetail.text = getString(R.string.hero_detail_idle)
            }
        }

        connectionText.text = when {
            !running -> getString(R.string.no_connections)
            connections == 1 -> getString(R.string.connections_one)
            else -> resources.getQuantityString(R.plurals.connections_many, connections, connections)
        }

        val vpnActive = NetInfo.vpnActive(this)
        vpnText.text = getString(if (vpnActive) R.string.vpn_active else R.string.vpn_inactive)
        vpnText.setTextColor(getColor(if (vpnActive) R.color.success else R.color.warning))
        vpnDot.setBackgroundResource(if (vpnActive) R.drawable.dot_connected else R.drawable.dot_connecting)

        val stats = TunnelStats.snapshot()
        val (downValue, downUnit) = Format.rate(if (running) stats.downloadRate else 0.0)
        val (upValue, upUnit) = Format.rate(if (running) stats.uploadRate else 0.0)
        downRateText.text = downValue
        downRateUnit.text = downUnit
        upRateText.text = upValue
        upRateUnit.text = upUnit
        downTotalText.text = getString(R.string.downloaded, Format.bytes(stats.downloadBytes))
        upTotalText.text = getString(R.string.uploaded, Format.bytes(stats.uploadBytes))
        sessionText.text = getString(R.string.session, Format.duration(stats.durationMillis))

        if (running) {
            downSpark.push(stats.downloadRate)
            upSpark.push(stats.uploadRate)
        } else {
            downSpark.push(0.0)
            upSpark.push(0.0)
        }

        // Connect button state (hero caption above carries the wording)
        startStopButton.setState(
            when {
                running && error != null -> ConnectButton.STATE_ERROR
                running && transports.isNotEmpty() -> ConnectButton.STATE_SHARING
                running -> ConnectButton.STATE_CONNECTING
                else -> ConnectButton.STATE_IDLE
            }
        )

        val ip = NetInfo.wifiIp(this)
        wifiIpText.text = ip ?: getString(R.string.wifi_not_connected)
        wifiIpText.setTextColor(getColor(if (ip != null) R.color.text else R.color.text_muted))
        wifiPortText.text = getString(R.string.wifi_port_default)
        // Without a Wi-Fi address the PC has nothing to connect to, so say why
        // instead of leaving a silent "Not connected".
        wifiAddressHint.isVisible = ip == null
        tokenText.text = Prefs.formatToken(token)

        logText.text = TunnelService.logsSnapshot()
        activitySummary.text = TunnelService.logsSnapshot()
            .lineSequence()
            .lastOrNull { it.isNotBlank() }
            ?: getString(R.string.no_activity_yet)

        val powerManager = getSystemService(PowerManager::class.java)
        val ignoring = powerManager.isIgnoringBatteryOptimizations(packageName)
        batteryValue.text = getString(
            if (ignoring) R.string.battery_ok else R.string.battery_value
        )
        batteryValue.setTextColor(getColor(if (ignoring) R.color.success else R.color.text_muted))
    }

    private fun toggleActivity() {
        activityOpen = !activityOpen
        logText.isVisible = activityOpen
        activityChevron.animate()
            .rotation(if (activityOpen) 180f else 0f)
            .setDuration(160L)
            .start()
    }

    private fun copyPassword() {
        val clipboard = getSystemService(ClipboardManager::class.java)
        clipboard.setPrimaryClip(
            ClipData.newPlainText("ShareNet password", Prefs.formatToken(token))
        )
        Toast.makeText(this, R.string.copied, Toast.LENGTH_SHORT).show()
    }

    private fun openBatterySettings() {
        val powerManager = getSystemService(PowerManager::class.java)
        if (powerManager.isIgnoringBatteryOptimizations(packageName)) {
            Toast.makeText(this, R.string.battery_ok, Toast.LENGTH_SHORT).show()
            return
        }

        val request = Intent(
            Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
            Uri.parse("package:$packageName")
        )
        try {
            startActivity(request)
        } catch (_: Exception) {
            startActivity(
                Intent(
                    Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                    Uri.parse("package:$packageName")
                )
            )
        }
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != REQUEST_HOTSPOT || !startAfterPermission) return

        startAfterPermission = false
        if (grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED) {
            startTunnel()
        } else {
            Toast.makeText(
                this,
                "Without this permission the phone cannot open its ShareNet hotspot. " +
                    "Join the same Wi-Fi network as the PC instead.",
                Toast.LENGTH_LONG
            ).show()
        }
    }

    private fun hotspotPermission(): String =
        if (Build.VERSION.SDK_INT >= 33) Manifest.permission.NEARBY_WIFI_DEVICES
        else Manifest.permission.ACCESS_FINE_LOCATION

    private fun hasHotspotPermission(): Boolean =
        checkSelfPermission(hotspotPermission()) == PackageManager.PERMISSION_GRANTED

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) !=
            PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 100)
        }
    }
}