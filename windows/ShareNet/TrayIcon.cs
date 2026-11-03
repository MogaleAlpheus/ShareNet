using System.Drawing;
using System.Runtime.InteropServices;
using Forms = System.Windows.Forms;

namespace ShareNet;

/// <summary>Notification-area icon and menu. Closing the window keeps the tunnel alive.</summary>
public sealed class TrayIcon : IDisposable
{
    private readonly TunnelController _controller;
    private readonly MainWindow _window;
    private readonly Func<Task> _exitRequested;
    private readonly Forms.NotifyIcon _notifyIcon;
    private readonly Forms.ToolStripMenuItem _usbItem;
    private readonly Forms.ToolStripMenuItem _wifiItem;
    private readonly Forms.ToolStripMenuItem _disconnectItem;
    private readonly Icon _icon;
    private TunnelState _lastState = TunnelState.Stopped;
    private DateTime _lastBalloon = DateTime.MinValue;
    private bool _disposed;

    public TrayIcon(TunnelController controller, MainWindow window, Func<Task> exitRequested)
    {
        _controller = controller;
        _window = window;
        _exitRequested = exitRequested;
        _icon = LoadBrandIcon();

        // The transport is chosen here as well as in the window, so starting a
        // session never means opening the app first.
        _usbItem = new Forms.ToolStripMenuItem(
            "Connect over USB cable", null, async (_, _) => await ConnectAsync("usb"));
        _wifiItem = new Forms.ToolStripMenuItem(
            "Connect over Wi-Fi", null, async (_, _) => await ConnectAsync("wifi"));
        _disconnectItem = new Forms.ToolStripMenuItem(
            "Disconnect", null, async (_, _) => await DisconnectAsync());

        var menu = new Forms.ContextMenuStrip();
        menu.Items.Add(new Forms.ToolStripMenuItem("Open ShareNet", null, (_, _) => ShowWindow()));
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add(_usbItem);
        menu.Items.Add(_wifiItem);
        menu.Items.Add(_disconnectItem);
        menu.Items.Add(new Forms.ToolStripSeparator());
        menu.Items.Add(new Forms.ToolStripMenuItem("Exit", null, async (_, _) => await ExitAsync()));

        _notifyIcon = new Forms.NotifyIcon
        {
            Icon = _icon,
            Text = "ShareNet",
            Visible = true,
            ContextMenuStrip = menu
        };

        _notifyIcon.DoubleClick += (_, _) => ShowWindow();
        _controller.Changed += OnControllerChanged;
        _controller.Traffic.Updated += OnTrafficUpdated;
        UpdateMenu();
    }

    public void ShowStillRunning()
    {
        if (_disposed || DateTime.UtcNow - _lastBalloon < TimeSpan.FromSeconds(8))
        {
            return;
        }

        ShowBalloon(
            "ShareNet is still running",
            "The tunnel stays active in the notification area. Right-click the icon to disconnect or exit.",
            Forms.ToolTipIcon.Info);
    }

    private void ShowWindow()
    {
        try
        {
            _window.Dispatcher.Invoke(_window.ShowFromTray);
        }
        catch
        {
            // The app is exiting.
        }
    }

    private async Task ConnectAsync(string transport)
    {
        try
        {
            await _window.ConnectTransportAsync(transport);
        }
        catch (Exception ex)
        {
            ShowBalloon("ShareNet", ex.Message, Forms.ToolTipIcon.Error);
        }
    }

    private async Task DisconnectAsync()
    {
        try
        {
            await _window.DisconnectFromTrayAsync();
        }
        catch (Exception ex)
        {
            ShowBalloon("ShareNet", ex.Message, Forms.ToolTipIcon.Error);
        }
    }

    private async Task ExitAsync()
    {
        try
        {
            await _exitRequested();
        }
        catch
        {
            // Shutting down.
        }
    }

    private void OnControllerChanged()
    {
        if (_disposed)
        {
            return;
        }

        try
        {
            _window.Dispatcher.InvokeAsync(UpdateMenu);
        }
        catch
        {
            // The app is exiting.
        }
    }

    private void OnTrafficUpdated()
    {
        if (_disposed)
        {
            return;
        }

        try
        {
            _window.Dispatcher.InvokeAsync(UpdateTrafficText);
        }
        catch
        {
            // The app is exiting.
        }
    }

    private void UpdateTrafficText()
    {
        if (_disposed)
        {
            return;
        }

        if (_controller.State != TunnelState.Connected)
        {
            return;
        }

        var stats = _controller.Traffic.Snapshot();
        var text = $"ShareNet — ↓ {FormatRate(stats.DownloadRate)}  ↑ {FormatRate(stats.UploadRate)}";
        if (text.Length > 63)
        {
            text = text[..63];
        }

        try
        {
            _notifyIcon.Text = text;
        }
        catch
        {
            // Notifications can be disabled by the user.
        }
    }

    private static string FormatRate(double bytesPerSecond)
    {
        if (bytesPerSecond < 1024)
        {
            return $"{bytesPerSecond:0} B/s";
        }
        if (bytesPerSecond < 1024 * 1024)
        {
            return $"{bytesPerSecond / 1024:0.0} KB/s";
        }
        return $"{bytesPerSecond / (1024 * 1024):0.0} MB/s";
    }

    private string TransportLabel =>
        _window.CurrentTransport.Equals("wifi", StringComparison.OrdinalIgnoreCase) ? "Wi-Fi" : "USB";

    private void UpdateMenu()
    {
        if (_disposed)
        {
            return;
        }

        var state = _controller.State;
        var busy = state is TunnelState.Connecting or TunnelState.Disconnecting;
        var connected = state == TunnelState.Connected;

        _usbItem.Enabled = !busy && !connected;
        _wifiItem.Enabled = !busy && !connected;
        _disconnectItem.Enabled = !busy && state != TunnelState.Stopped;

        // A check mark shows which transport the live session is using.
        var transport = _window.CurrentTransport;
        _usbItem.Checked = connected && !transport.Equals("wifi", StringComparison.OrdinalIgnoreCase);
        _wifiItem.Checked = connected && transport.Equals("wifi", StringComparison.OrdinalIgnoreCase);

        _disconnectItem.Text = state switch
        {
            TunnelState.Connecting => "Connecting…",
            TunnelState.Disconnecting => "Disconnecting…",
            _ => "Disconnect"
        };

        _notifyIcon.Text = state switch
        {
            TunnelState.Connected => Truncate($"ShareNet — connected over {TransportLabel}"),
            TunnelState.Connecting => "ShareNet — connecting",
            TunnelState.Disconnecting => "ShareNet — disconnecting",
            TunnelState.Error => "ShareNet — action needed",
            _ => "ShareNet"
        };
        UpdateTrafficText();

        if (state != _lastState && !_window.IsVisible)
        {
            switch (state)
            {
                case TunnelState.Connected:
                    ShowBalloon(
                        "ShareNet connected",
                        $"Windows traffic is now using the phone's connection over {TransportLabel}.",
                        Forms.ToolTipIcon.Info);
                    break;
                case TunnelState.Error:
                    ShowBalloon(
                        "ShareNet disconnected",
                        _controller.LastError ?? "The tunnel stopped unexpectedly.",
                        Forms.ToolTipIcon.Error);
                    break;
            }
        }

        _lastState = state;
    }

    private static string Truncate(string text) => text.Length > 63 ? text[..63] : text;

    private void ShowBalloon(string title, string text, Forms.ToolTipIcon icon)
    {
        _lastBalloon = DateTime.UtcNow;
        try
        {
            _notifyIcon.ShowBalloonTip(4000, title, text, icon);
        }
        catch
        {
            // Notifications can be disabled by the user.
        }
    }

    /// <summary>
    /// The packaged brand icon: the same navy tile with the gradient "S" the
    /// launcher and the window use. It is read from the embedded ShareNet.ico so
    /// the tray can never drift back to an older drawn mark.
    /// </summary>
    private static Icon LoadBrandIcon()
    {
        var size = Forms.SystemInformation.SmallIconSize;
        try
        {
            var resource = System.Windows.Application.GetResourceStream(
                new Uri("ShareNet.ico", UriKind.Relative));
            if (resource?.Stream is { } stream)
            {
                using (stream)
                {
                    return new Icon(stream, size);
                }
            }
        }
        catch
        {
            // Fall through to the executable's own icon.
        }

        try
        {
            if (Environment.ProcessPath is { } executable)
            {
                var extracted = Icon.ExtractAssociatedIcon(executable);
                if (extracted is not null)
                {
                    using (extracted)
                    {
                        return new Icon(extracted, size);
                    }
                }
            }
        }
        catch
        {
            // Fall through to the generic icon.
        }

        return (Icon)SystemIcons.Application.Clone();
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }
        _disposed = true;

        try
        {
            _controller.Changed -= OnControllerChanged;
            _controller.Traffic.Updated -= OnTrafficUpdated;
        }
        catch
        {
            // ignore
        }

        _notifyIcon.Visible = false;
        _notifyIcon.Dispose();
        _icon.Dispose();
    }
}
