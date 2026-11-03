using System.Diagnostics;

namespace ShareNet;

public readonly record struct TrafficSnapshot(
    long DownloadBytes,
    long UploadBytes,
    double DownloadRate,
    double UploadRate,
    double PeakDownloadRate,
    double PeakUploadRate,
    TimeSpan Duration,
    bool Active);

/// <summary>
/// Counts the bytes that the SOCKS bridge relays and samples the rate twice a
/// second. Counters are per connection session and are never truncated, so long
/// transfers keep an accurate total.
/// </summary>
public sealed class TrafficStats : IDisposable
{
    private const int SampleMilliseconds = 500;

    private long _download;
    private long _upload;
    private long _lastDownload;
    private long _lastUpload;
    private long _sampleTimestamp;
    private long _downloadRate;
    private long _uploadRate;
    private long _peakDownloadRate;
    private long _peakUploadRate;
    private long _startedTicks;
    private long _endedTicks;
    private int _active;
    private Timer? _timer;

    /// <summary>Raised on a background thread after every sampling tick.</summary>
    public event Action? Updated;

    public long DownloadBytes => Interlocked.Read(ref _download);

    public long UploadBytes => Interlocked.Read(ref _upload);

    public bool IsActive => Volatile.Read(ref _active) == 1;

    public void AddDownload(int bytes)
    {
        if (bytes > 0)
        {
            Interlocked.Add(ref _download, bytes);
        }
    }

    public void AddUpload(int bytes)
    {
        if (bytes > 0)
        {
            Interlocked.Add(ref _upload, bytes);
        }
    }

    public void StartSession()
    {
        Interlocked.Exchange(ref _download, 0);
        Interlocked.Exchange(ref _upload, 0);
        Interlocked.Exchange(ref _lastDownload, 0);
        Interlocked.Exchange(ref _lastUpload, 0);
        Interlocked.Exchange(ref _downloadRate, 0);
        Interlocked.Exchange(ref _uploadRate, 0);
        Interlocked.Exchange(ref _peakDownloadRate, 0);
        Interlocked.Exchange(ref _peakUploadRate, 0);
        Interlocked.Exchange(ref _endedTicks, 0);
        Interlocked.Exchange(ref _startedTicks, DateTime.UtcNow.Ticks);
        Interlocked.Exchange(ref _sampleTimestamp, Stopwatch.GetTimestamp());
        Volatile.Write(ref _active, 1);

        _timer ??= new Timer(_ => Tick(), null, SampleMilliseconds, SampleMilliseconds);
    }

    public void StopSession()
    {
        if (Interlocked.Exchange(ref _active, 0) == 0)
        {
            return;
        }

        Interlocked.Exchange(ref _endedTicks, DateTime.UtcNow.Ticks);
        Interlocked.Exchange(ref _downloadRate, 0);
        Interlocked.Exchange(ref _uploadRate, 0);
    }

    private void Tick()
    {
        if (!IsActive)
        {
            return;
        }

        var now = Stopwatch.GetTimestamp();
        var last = Interlocked.Read(ref _sampleTimestamp);
        var elapsed = (now - last) / (double)Stopwatch.Frequency;
        if (elapsed < 0.05)
        {
            return;
        }
        Interlocked.Exchange(ref _sampleTimestamp, now);

        var download = DownloadBytes;
        var upload = UploadBytes;
        var downloadRate = (long)Math.Max(0, (download - Interlocked.Read(ref _lastDownload)) / elapsed);
        var uploadRate = (long)Math.Max(0, (upload - Interlocked.Read(ref _lastUpload)) / elapsed);

        Interlocked.Exchange(ref _lastDownload, download);
        Interlocked.Exchange(ref _lastUpload, upload);
        Interlocked.Exchange(ref _downloadRate, downloadRate);
        Interlocked.Exchange(ref _uploadRate, uploadRate);

        if (downloadRate > Interlocked.Read(ref _peakDownloadRate))
        {
            Interlocked.Exchange(ref _peakDownloadRate, downloadRate);
        }
        if (uploadRate > Interlocked.Read(ref _peakUploadRate))
        {
            Interlocked.Exchange(ref _peakUploadRate, uploadRate);
        }

        try
        {
            Updated?.Invoke();
        }
        catch
        {
            // A UI listener must never stop the sampler.
        }
    }

    public TrafficSnapshot Snapshot()
    {
        var start = Interlocked.Read(ref _startedTicks);
        var end = Interlocked.Read(ref _endedTicks);
        if (end == 0)
        {
            end = DateTime.UtcNow.Ticks;
        }

        var duration = start == 0 ? TimeSpan.Zero : TimeSpan.FromTicks(Math.Max(0, end - start));

        return new TrafficSnapshot(
            DownloadBytes,
            UploadBytes,
            Interlocked.Read(ref _downloadRate),
            Interlocked.Read(ref _uploadRate),
            Interlocked.Read(ref _peakDownloadRate),
            Interlocked.Read(ref _peakUploadRate),
            duration,
            IsActive);
    }

    public void Dispose()
    {
        Volatile.Write(ref _active, 0);
        _timer?.Dispose();
        _timer = null;
    }
}
