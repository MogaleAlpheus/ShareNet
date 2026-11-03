using System.ComponentModel;
using System.Globalization;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Input;
using System.Windows.Interop;
using System.Windows.Media;

namespace ShareNet;

public partial class MainWindow : Window
{
    private readonly TunnelController _controller;
    private readonly AppSettings _settings;
    private bool _initialized;
    private bool _allowClose;
    private bool _refreshingDevices;
    private bool _searchingPhone;
    private string _lastLogLine = string.Empty;
    private string _deviceHint = string.Empty;
    private readonly List<AdapterOption> _adapterOptions = new();

    public MainWindow(TunnelController controller)
    {
        InitializeComponent();

        _deviceHint = UsbHint.Text;
        _controller = controller;
        _settings = SettingsStore.Load();

        _controller.Changed += OnControllerChanged;
        _controller.LogLine += OnLogLine;
        _controller.Traffic.Updated += OnTrafficUpdated;

        LoadAdapters();
        LoadSettings();

        DeviceCombo.SelectionChanged += (_, _) => SaveCurrentSettings();
        AdapterCombo.SelectionChanged += (_, _) => SaveCurrentSettings();
        HostBox.LostFocus += (_, _) => SaveCurrentSettings();
        PasswordInput.LostFocus += (_, _) => SaveCurrentSettings();

        LogBox.Text = string.IsNullOrWhiteSpace(_controller.LogSnapshot)
            ? "No activity yet."
            : _controller.LogSnapshot;
        LogBox.ScrollToEnd();

        _initialized = true;
        UpdateModePanels();
        UpdateUi();
        UpdateTraffic();

        _ = RefreshDevicesAsync();
    }

    protected override void OnSourceInitialized(EventArgs e)
    {
        base.OnSourceInitialized(e);
        TryEnableDarkTitleBar();
    }

    public void ShowFromTray()
    {
        Show();
        if (WindowState == WindowState.Minimized)
        {
            WindowState = WindowState.Normal;
        }

        Activate();
        Topmost = true;
        Topmost = false;
        Focus();
    }

    public async Task ToggleTunnelAsync()
    {
        if (_controller.State == TunnelState.Connected)
        {
            await _controller.DisconnectAsync();
            return;
        }

        if (_controller.IsBusy)
        {
            return;
        }

        await StartTunnelAsync();
    }

    public async Task AutoConnectAsync()
    {
        if (UsbModeButton.IsChecked == true && _controller.State == TunnelState.Stopped)
        {
            await ToggleTunnelAsync();
        }
    }

    /// <summary>The transport the window is set to, as the tray menu labels it.</summary>
    public string CurrentTransport => UsbModeButton.IsChecked == true ? "usb" : "wifi";

    /// <summary>
    /// Starts a session on one named transport, for the tray menu. The password
    /// comes from the saved settings, so Wi-Fi can start without opening the
    /// window; the window only appears when there is nothing to connect with.
    /// </summary>
    public async Task ConnectTransportAsync(string transport)
    {
        if (_controller.IsBusy || _controller.State == TunnelState.Connected)
        {
            return;
        }

        var wifi = transport.Equals("wifi", StringComparison.OrdinalIgnoreCase);
        if (wifi)
        {
            WifiModeButton.IsChecked = true;
        }
        else
        {
            UsbModeButton.IsChecked = true;
        }

        UpdateModePanels();
        SaveCurrentSettings();

        if (wifi && PasswordInput.Password.Length == 0)
        {
            ShowFromTray();
            WifiHint.Text = "Enter the password shown in the ShareNet app on the phone, then press Connect. It is saved for next time.";
            PasswordInput.Focus();
            return;
        }

        await StartTunnelAsync();
    }

    /// <summary>Tray menu disconnect, independent of the window's own button.</summary>
    public async Task DisconnectFromTrayAsync()
    {
        if (_controller.State is TunnelState.Connected or TunnelState.Error)
        {
            await _controller.DisconnectAsync();
        }
    }

    public void CloseForExit()
    {
        _allowClose = true;
        Close();
    }

    private void LoadSettings()
    {
        HostBox.Text = _settings.WifiHost;
        PasswordInput.Password = _settings.WifiPassword;

        if (_settings.Mode.Equals("wifi", StringComparison.OrdinalIgnoreCase))
        {
            WifiModeButton.IsChecked = true;
        }
        else
        {
            UsbModeButton.IsChecked = true;
        }

        var selected = _adapterOptions.FirstOrDefault(option =>
                            option.Name.Equals(_settings.AdapterName, StringComparison.OrdinalIgnoreCase))
                        ?? _adapterOptions[0];
        AdapterCombo.SelectedItem = selected;
    }

    private void LoadAdapters()
    {
        _adapterOptions.Clear();
        _adapterOptions.Add(new AdapterOption(string.Empty, "Automatic"));

        foreach (var adapter in NetworkHelper.GetPhysicalAdapters())
        {
            _adapterOptions.Add(new AdapterOption(adapter.Name, adapter.DisplayName));
        }

        AdapterCombo.ItemsSource = null;
        AdapterCombo.ItemsSource = _adapterOptions;
        AdapterCombo.SelectedIndex = 0;
    }

    private void SaveCurrentSettings()
    {
        if (!_initialized)
        {
            return;
        }

        _settings.Mode = WifiModeButton.IsChecked == true ? "wifi" : "usb";
        _settings.WifiHost = HostBox.Text.Trim();
        _settings.WifiPassword = PasswordInput.Password;
        if (AdapterCombo.SelectedItem is AdapterOption adapter)
        {
            _settings.AdapterName = adapter.Name;
        }
        if (DeviceCombo.SelectedItem is AdbDevice device && !string.IsNullOrEmpty(device.Serial))
        {
            _settings.LastSerial = device.Serial;
        }

        SettingsStore.Save(_settings);
    }

    private async Task StartTunnelAsync()
    {
        SaveCurrentSettings();

        if (UsbModeButton.IsChecked == true)
        {
            var serial = (DeviceCombo.SelectedItem as AdbDevice)?.Serial;
            await _controller.ConnectUsbAsync(serial);
            return;
        }

        var password = PasswordInput.Password;
        if (password.Length == 0)
        {
            System.Windows.MessageBox.Show(
                this,
                "Enter the password shown in the ShareNet app on the phone.",
                "ShareNet",
                MessageBoxButton.OK,
                MessageBoxImage.Information);
            PasswordInput.Focus();
            return;
        }

        var adapterName = (AdapterCombo.SelectedItem as AdapterOption)?.Name;
        var selectedAdapter = string.IsNullOrWhiteSpace(adapterName) ? null : adapterName;
        var host = HostBox.Text.Trim();

        if (host.Length == 0)
        {
            // No address: search the network, and if the phone is not there, join
            // the hotspot it opened. The password is the only thing to type.
            await _controller.ConnectWifiAutoAsync(password, selectedAdapter);
            return;
        }

        await _controller.ConnectWifiAsync(host, password, selectedAdapter);
    }

    private async Task RefreshDevicesAsync()
    {
        if (_refreshingDevices || _controller.IsBusy)
        {
            return;
        }

        _refreshingDevices = true;
        RefreshButton.IsEnabled = false;
        DeviceCombo.IsEnabled = false;
        var previousSerial = DeviceCombo.SelectedItem is AdbDevice selectedDevice &&
                             !string.IsNullOrEmpty(selectedDevice.Serial)
            ? selectedDevice.Serial
            : _settings.LastSerial;
        DeviceCombo.Items.Clear();
        DeviceCombo.Items.Add(AdbDevice.Placeholder("Looking for phones…"));
        DeviceCombo.SelectedIndex = 0;
        SetDeviceHint("Starting adb and checking for connected devices…");

        try
        {
            var devices = await _controller.GetUsbDevicesAsync(CancellationToken.None);

            DeviceCombo.Items.Clear();
            if (devices.Count == 0)
            {
                DeviceCombo.Items.Add(AdbDevice.Placeholder("No phone found"));
                DeviceCombo.SelectedIndex = 0;
                SetDeviceHint("No Android phone found. Connect the USB cable, enable USB debugging, and accept the prompt on the phone.");
            }
            else
            {
                foreach (var device in devices)
                {
                    DeviceCombo.Items.Add(device);
                }

                var selected = devices.FirstOrDefault(d =>
                                   d.Serial.Equals(previousSerial, StringComparison.OrdinalIgnoreCase))
                               ?? devices.FirstOrDefault(d => d.IsUsable)
                               ?? devices[0];
                DeviceCombo.SelectedItem = selected;

                var blocked = devices.FirstOrDefault(d => !d.IsUsable);
                SetDeviceHint(blocked is null
                    ? "Phone found. Click Connect."
                    : blocked.State.Equals("unauthorized", StringComparison.OrdinalIgnoreCase)
                        ? "Unlock the phone and tap Allow on the USB debugging prompt, then press Refresh."
                        : $"The phone is not ready ({blocked.State}). Reconnect the cable and press Refresh.");
            }
        }
        catch (Exception ex)
        {
            DeviceCombo.Items.Clear();
            DeviceCombo.Items.Add(AdbDevice.Placeholder("USB unavailable"));
            DeviceCombo.SelectedIndex = 0;
            SetDeviceHint(ex.Message);
        }
        finally
        {
            _refreshingDevices = false;
            DeviceCombo.IsEnabled = !_controller.IsBusy;
            RefreshButton.IsEnabled = !_controller.IsBusy;
            UpdateUi();
        }
    }

    private void Mode_Changed(object sender, RoutedEventArgs e)
    {
        if (!_initialized)
        {
            return;
        }

        UpdateModePanels();
        SaveCurrentSettings();

        if (WifiModeButton.IsChecked == true && _controller.State is TunnelState.Stopped or TunnelState.Error)
        {
            var rememberedAdapter = _settings.AdapterName;
            LoadAdapters();
            AdapterCombo.SelectedItem = _adapterOptions.FirstOrDefault(option =>
                                         option.Name.Equals(rememberedAdapter, StringComparison.OrdinalIgnoreCase))
                                     ?? _adapterOptions[0];

            if (string.IsNullOrWhiteSpace(HostBox.Text))
            {
                _ = FindPhoneAsync(auto: true);
            }
        }
        else if (UsbModeButton.IsChecked == true && _controller.State is TunnelState.Stopped or TunnelState.Error)
        {
            _ = RefreshDevicesAsync();
        }

        UpdateUi();
    }

    private async void FindPhoneButton_Click(object sender, RoutedEventArgs e)
    {
        await FindPhoneAsync(auto: false);
    }

    /// <summary>
    /// Finds the phone on the local network so the address never has to be typed.
    /// The beacon is tried first, then an outbound sweep of the adapter's subnet,
    /// which needs no firewall rule. A found address is still just a candidate:
    /// the connection has to pass the password handshake.
    /// </summary>
    private async Task FindPhoneAsync(bool auto)
    {
        if (_searchingPhone || _controller.IsBusy)
        {
            return;
        }

        _searchingPhone = true;
        FindPhoneButton.IsEnabled = false;
        WifiHint.Text = auto
            ? "Looking for the phone on this network…"
            : "Searching this network for the phone…";

        try
        {
            var adapters = NetworkHelper.GetPhysicalAdapters();
            var adapterName = (AdapterCombo.SelectedItem as AdapterOption)?.Name;
            PhysicalAdapter? adapter = string.IsNullOrWhiteSpace(adapterName)
                ? null
                : adapters.FirstOrDefault(a =>
                    a.Name.Equals(adapterName, StringComparison.OrdinalIgnoreCase));
            // Prefer the adapter that actually carries the default route: the
            // phone is a neighbour on that link, not on a Hyper-V or VPN NIC.
            adapter ??= adapters.FirstOrDefault(a => a.IsWireless && a.Gateway is not null)
                        ?? adapters.FirstOrDefault(a => a.Gateway is not null)
                        ?? adapters.FirstOrDefault(a => a.IsWireless)
                        ?? adapters.FirstOrDefault();

            if (adapter is null)
            {
                WifiHint.Text = "No network adapter is up. Connect the PC to Wi-Fi (or the phone's hotspot) first.";
                return;
            }

            var found = await LanDiscovery.ScanAsync(
                adapter,
                TimeSpan.FromSeconds(auto ? 4.5 : 7.0));

            if (found.Count == 0)
            {
                WifiHint.Text =
                    $"No phone found on {adapter.Name}. Start sharing over Wi-Fi in the phone app, " +
                    "make sure both devices are on the same network, then press Find again.";
                return;
            }

            var phone = found[0];
            HostBox.Text = phone.Address;
            SaveCurrentSettings();
            WifiHint.Text = found.Count == 1
                ? $"Found {phone.Model} at {phone.Address}. Enter the password to connect."
                : $"Found {found.Count} candidates; selected {phone.Address}. Enter the password to connect.";

            if (PasswordInput.Password.Length == 0)
            {
                PasswordInput.Focus();
            }
        }
        catch (Exception ex)
        {
            WifiHint.Text = $"Search failed: {ex.Message}";
        }
        finally
        {
            _searchingPhone = false;
            FindPhoneButton.IsEnabled = !_controller.IsBusy;
        }
    }

    private void UpdateModePanels()
    {
        var usb = UsbModeButton.IsChecked == true;
        UsbPanel.Visibility = usb ? Visibility.Visible : Visibility.Collapsed;
        WifiPanel.Visibility = usb ? Visibility.Collapsed : Visibility.Visible;
        TransportText.Text = usb ? "USB cable" : "Wi-Fi";
    }

    private async void ConnectButton_Click(object sender, RoutedEventArgs e)
    {
        try
        {
            await ToggleTunnelAsync();
        }
        catch (Exception ex)
        {
            System.Windows.MessageBox.Show(
                this,
                ex.Message,
                "ShareNet",
                MessageBoxButton.OK,
                MessageBoxImage.Error);
        }
    }

    private async void RefreshButton_Click(object sender, RoutedEventArgs e)
    {
        await RefreshDevicesAsync();
    }

    private void ActivityToggle_Click(object sender, RoutedEventArgs e)
    {
        var expand = LogBox.Visibility != Visibility.Visible;
        LogBox.Visibility = expand ? Visibility.Visible : Visibility.Collapsed;
        if (ActivityChevron.RenderTransform is RotateTransform rotate)
        {
            rotate.Angle = expand ? 180 : 0;
        }
    }

    private void SetDeviceHint(string text)
    {
        _deviceHint = text;
        if (_controller.State is not (TunnelState.Connecting or TunnelState.Connected or TunnelState.Disconnecting))
        {
            UsbHint.Text = text;
        }
    }

    private void OnControllerChanged()
    {
        Dispatcher.InvokeAsync(UpdateUi);
    }

    private void OnTrafficUpdated()
    {
        Dispatcher.InvokeAsync(UpdateTraffic);
    }

    private void OnLogLine(string line)
    {
        Dispatcher.InvokeAsync(() => AppendLog(line));
    }

    private void AppendLog(string line)
    {
        if (LogBox.Text == "No activity yet.")
        {
            LogBox.Clear();
        }

        LogBox.AppendText(line + Environment.NewLine);
        if (LogBox.Text.Length > 200_000)
        {
            LogBox.Text = LogBox.Text[^120_000..];
        }
        LogBox.ScrollToEnd();

        _lastLogLine = line;
        ActivitySummary.Text = line.Length > 64 ? line[..64] + "…" : line;
    }

    private void UpdateUi()
    {
        var state = _controller.State;

        var (pill, brushKey, dotKey) = state switch
        {
            TunnelState.Connected => ("Sharing", "SuccessSoftBrush", "SuccessBrush"),
            TunnelState.Connecting => ("Connecting", "WarningSoftBrush", "WarningBrush"),
            TunnelState.Disconnecting => ("Disconnecting", "WarningSoftBrush", "WarningBrush"),
            TunnelState.Error => ("Action needed", "ErrorSoftBrush", "ErrorBrush"),
            _ => ("Stopped", "Surface2Brush", "TextMutedBrush")
        };

        StatusPillText.Text = pill;
        StatusPill.Background = (Brush)FindResource(brushKey);
        StatusDot.Fill = (Brush)FindResource(dotKey);

        StatusText.Text = _controller.Status;
        StatusDetail.Text = state switch
        {
            TunnelState.Connected =>
                "All Windows traffic is using the phone's connection. No proxy settings were changed.",
            TunnelState.Connecting =>
                "Setting up the phone link, virtual adapter, routes, and DNS.",
            TunnelState.Disconnecting =>
                "Restoring the normal Windows network configuration.",
            TunnelState.Error =>
                "The tunnel is not active. Fix the issue and try again.",
            _ =>
                "Choose USB or Wi-Fi below. Every Windows app will use the phone's connection."
        };

        if (!string.IsNullOrWhiteSpace(_controller.LastError))
        {
            ErrorText.Text = _controller.LastError;
            ErrorText.Visibility = Visibility.Visible;
        }
        else
        {
            ErrorText.Visibility = Visibility.Collapsed;
        }

        ConnectButton.Content = state switch
        {
            TunnelState.Connected => "Disconnect",
            TunnelState.Connecting => "Connecting…",
            TunnelState.Disconnecting => "Disconnecting…",
            _ => "Connect"
        };
        ConnectButton.IsEnabled = state is not (TunnelState.Connecting or TunnelState.Disconnecting);
        ConnectButton.Style = (Style)FindResource(
            state == TunnelState.Connected ? "DangerButtonStyle" : "PrimaryButtonStyle");

        var locked = state is TunnelState.Connecting or TunnelState.Connected or TunnelState.Disconnecting;
        UsbModeButton.IsEnabled = !locked;
        WifiModeButton.IsEnabled = !locked;
        DeviceCombo.IsEnabled = !locked && !_refreshingDevices;
        RefreshButton.IsEnabled = !locked && !_refreshingDevices;
        HostBox.IsEnabled = !locked;
        FindPhoneButton.IsEnabled = !locked && !_searchingPhone;
        PasswordInput.IsEnabled = !locked;
        AdapterCombo.IsEnabled = !locked;

        // The setup hint must describe the live state, not the last scan result.
        if (state is TunnelState.Connected)
        {
            UsbHint.Text = "Connected. Windows traffic is routed through this phone.";
        }
        else if (state is TunnelState.Connecting or TunnelState.Disconnecting)
        {
            UsbHint.Text = state is TunnelState.Connecting ? "Connecting to the phone…" : "Disconnecting…";
        }
        else if (!_refreshingDevices && !string.IsNullOrEmpty(_deviceHint))
        {
            UsbHint.Text = _deviceHint;
        }

        var isAdmin = SecurityHelper.IsAdministrator();
        AdminBadgeText.Text = isAdmin ? "ADMIN" : "NOT ADMIN";
        AdminBadge.Background = isAdmin
            ? (Brush)FindResource("SuccessSoftBrush")
            : (Brush)FindResource("Surface2Brush");
        AdminBadgeText.Foreground = isAdmin
            ? (Brush)FindResource("SuccessBrush")
            : (Brush)FindResource("TextMutedBrush");

        if (!_initialized)
        {
            return;
        }

        if (state is TunnelState.Connected)
        {
            UpdateTraffic();
        }
        else if (state is TunnelState.Stopped or TunnelState.Error)
        {
            DownSpark.Reset();
            UpSpark.Reset();
            DownRateText.Text = "0.0";
            DownRateUnit.Text = "B/s";
            UpRateText.Text = "0.0";
            UpRateUnit.Text = "B/s";
        }
    }

    private void UpdateTraffic()
    {
        var stats = _controller.Traffic.Snapshot();

        var (downValue, downUnit) = FormatRate(stats.DownloadRate);
        var (upValue, upUnit) = FormatRate(stats.UploadRate);
        DownRateText.Text = downValue;
        DownRateUnit.Text = downUnit;
        UpRateText.Text = upValue;
        UpRateUnit.Text = upUnit;

        DownTotalText.Text = FormatBytes(stats.DownloadBytes);
        UpTotalText.Text = FormatBytes(stats.UploadBytes);
        SessionText.Text = FormatDuration(stats.Duration);

        if (stats.Active)
        {
            DownSpark.Push(stats.DownloadRate);
            UpSpark.Push(stats.UploadRate);
        }
    }

    private static (string Value, string Unit) FormatRate(double bytesPerSecond)
    {
        if (bytesPerSecond < 1024)
        {
            return (bytesPerSecond.ToString(bytesPerSecond < 100 ? "0.0" : "0", CultureInfo.InvariantCulture), "B/s");
        }

        if (bytesPerSecond < 1024 * 1024)
        {
            var value = bytesPerSecond / 1024;
            return (value.ToString(value < 100 ? "0.0" : "0", CultureInfo.InvariantCulture), "KB/s");
        }

        if (bytesPerSecond < 1024 * 1024 * 1024)
        {
            var value = bytesPerSecond / (1024 * 1024);
            return (value.ToString(value < 100 ? "0.0" : "0", CultureInfo.InvariantCulture), "MB/s");
        }

        return ((bytesPerSecond / (1024 * 1024 * 1024)).ToString("0.00", CultureInfo.InvariantCulture), "GB/s");
    }

    private static string FormatBytes(long bytes)
    {
        string[] units = { "B", "KB", "MB", "GB", "TB" };
        var value = (double)Math.Max(0, bytes);
        var index = 0;
        while (value >= 1024 && index < units.Length - 1)
        {
            value /= 1024;
            index++;
        }

        var text = index == 0
            ? value.ToString("0", CultureInfo.InvariantCulture)
            : value.ToString("0.0", CultureInfo.InvariantCulture);
        return $"{text} {units[index]}";
    }

    private static string FormatDuration(TimeSpan duration)
    {
        if (duration <= TimeSpan.Zero)
        {
            return "00:00";
        }

        return duration.TotalHours >= 1
            ? string.Format(CultureInfo.InvariantCulture, "{0}:{1:00}:{2:00}", (int)duration.TotalHours, duration.Minutes, duration.Seconds)
            : string.Format(CultureInfo.InvariantCulture, "{0:00}:{1:00}", duration.Minutes, duration.Seconds);
    }

    private void TitleBar_MouseLeftButtonDown(object sender, MouseButtonEventArgs e)
    {
        if (e.ClickCount == 2)
        {
            ToggleMaximize();
            return;
        }

        if (e.ButtonState == MouseButtonState.Pressed)
        {
            try
            {
                DragMove();
            }
            catch (InvalidOperationException)
            {
                // The mouse was released between the press and the drag.
            }
        }
    }

    private void MinimizeButton_Click(object sender, RoutedEventArgs e)
    {
        WindowState = WindowState.Minimized;
    }

    private void MaximizeButton_Click(object sender, RoutedEventArgs e)
    {
        ToggleMaximize();
    }

    private void CloseButton_Click(object sender, RoutedEventArgs e)
    {
        Close();
    }

    private void ToggleMaximize()
    {
        WindowState = WindowState == WindowState.Maximized
            ? WindowState.Normal
            : WindowState.Maximized;
    }

    private void Window_StateChanged(object? sender, EventArgs e)
    {
        // A window with custom chrome draws under the resize border when maximized,
        // so inset the root frame by one border width.
        var inset = WindowState == WindowState.Maximized ? 7 : 0;
        RootFrame.BorderThickness = new Thickness(inset);

        if (MaximizeGlyph is not null)
        {
            MaximizeGlyph.Data = (Geometry)FindResource(
                WindowState == WindowState.Maximized ? "IconRestore" : "IconMaximize");
        }
    }

    private void Window_Closing(object? sender, CancelEventArgs e)
    {
        if (_allowClose)
        {
            return;
        }

        if (System.Windows.Application.Current is App app && !app.IsExiting)
        {
            e.Cancel = true;
            SaveCurrentSettings();
            Hide();
            app.NotifyWindowHidden();
        }
    }

    protected override void OnClosed(EventArgs e)
    {
        _controller.Changed -= OnControllerChanged;
        _controller.LogLine -= OnLogLine;
        _controller.Traffic.Updated -= OnTrafficUpdated;
        base.OnClosed(e);
    }

    private void TryEnableDarkTitleBar()
    {
        try
        {
            var handle = new WindowInteropHelper(this).Handle;
            if (handle == IntPtr.Zero)
            {
                return;
            }

            var enabled = 1;
            if (DwmSetWindowAttribute(handle, 20, ref enabled, sizeof(int)) != 0)
            {
                DwmSetWindowAttribute(handle, 19, ref enabled, sizeof(int));
            }

            // DWMWA_WINDOW_CORNER_PREFERENCE (33) with DWMWCP_ROUND (2) keeps the
            // custom-chrome window matching native rounded Windows 11 windows.
            var round = 2;
            DwmSetWindowAttribute(handle, 33, ref round, sizeof(int));
        }
        catch
        {
            // Older Windows builds keep the default title bar.
        }
    }

    [DllImport("dwmapi.dll", PreserveSig = true)]
    private static extern int DwmSetWindowAttribute(IntPtr hwnd, int attribute, ref int value, int size);

    private sealed record AdapterOption(string Name, string Display);
}
