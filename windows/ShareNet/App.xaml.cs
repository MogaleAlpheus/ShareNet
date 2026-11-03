using System.Windows;
using System.Windows.Threading;
using WpfApplication = System.Windows.Application;

namespace ShareNet;

public partial class App : WpfApplication
{
    private Mutex? _instanceMutex;
    private TunnelController? _controller;
    private MainWindow? _window;
    private TrayIcon? _tray;
    private bool _exiting;

    public bool IsExiting => _exiting;

    protected override void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);

        // Remote desktops and some virtual machines cannot use the hardware
        // renderer; this switch keeps the window usable there.
        if (Environment.GetEnvironmentVariable("SHARENET_SOFTWARE_RENDER") == "1")
        {
            System.Windows.Media.RenderOptions.ProcessRenderMode =
                System.Windows.Interop.RenderMode.SoftwareOnly;
        }

        _instanceMutex = new Mutex(
            initiallyOwned: true,
            name: @"Local\ShareNet.SingleInstance",
            createdNew: out var createdNew);

        if (!createdNew)
        {
            System.Windows.MessageBox.Show(
                "ShareNet is already running. Open it from the notification area.",
                "ShareNet",
                MessageBoxButton.OK,
                MessageBoxImage.Information);
            Shutdown();
            return;
        }

        DispatcherUnhandledException += OnDispatcherUnhandledException;
        AppDomain.CurrentDomain.UnhandledException += OnDomainUnhandledException;

        _controller = new TunnelController();
        _window = new MainWindow(_controller);
        _tray = new TrayIcon(_controller, _window, RequestExitAsync);
        _window.Show();

        _ = _controller.CleanupStaleAsync();

        if (e.Args.Any(arg => arg.Equals("--autoconnect", StringComparison.OrdinalIgnoreCase)))
        {
            _ = _window.AutoConnectAsync();
        }
    }

    public void NotifyWindowHidden()
    {
        _tray?.ShowStillRunning();
    }

    public void ShowMainWindow()
    {
        _window?.ShowFromTray();
    }

    public async Task RequestExitAsync()
    {
        if (_exiting)
        {
            return;
        }
        _exiting = true;

        try
        {
            if (_controller is not null)
            {
                await _controller.DisconnectAsync().WaitAsync(TimeSpan.FromSeconds(15));
            }
        }
        catch (Exception ex)
        {
            _controller?.AddLog($"Shutdown warning: {ex.Message}");
        }

        _tray?.Dispose();
        _tray = null;
        _window?.CloseForExit();
        Shutdown();
    }

    protected override void OnSessionEnding(SessionEndingCancelEventArgs e)
    {
        try
        {
            _controller?.DisconnectAsync().Wait(TimeSpan.FromSeconds(8));
        }
        catch
        {
            // Windows is shutting down; best effort only.
        }

        base.OnSessionEnding(e);
    }

    protected override void OnExit(ExitEventArgs e)
    {
        if (!_exiting)
        {
            try
            {
                _controller?.DisconnectAsync().Wait(TimeSpan.FromSeconds(15));
            }
            catch
            {
                // Best effort.
            }
        }

        _tray?.Dispose();
        _tray = null;

        try
        {
            _instanceMutex?.ReleaseMutex();
        }
        catch
        {
            // Not owned.
        }
        _instanceMutex?.Dispose();

        base.OnExit(e);
    }

    private void OnDispatcherUnhandledException(object sender, DispatcherUnhandledExceptionEventArgs e)
    {
        _controller?.AddLog($"UI error: {e.Exception.Message}");
        System.Windows.MessageBox.Show(
            $"ShareNet encountered an unexpected error:\n\n{e.Exception.Message}",
            "ShareNet",
            MessageBoxButton.OK,
            MessageBoxImage.Error);
        e.Handled = true;
    }

    private void OnDomainUnhandledException(object sender, UnhandledExceptionEventArgs e)
    {
        if (e.ExceptionObject is Exception exception)
        {
            _controller?.AddLog($"Fatal error: {exception.Message}");
        }
    }
}
