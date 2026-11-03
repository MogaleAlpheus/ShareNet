using System;
using System.Collections.Concurrent;
using System.Text;

namespace ShareNet;

/// <summary>Thread-safe rolling log used by the UI and for basic diagnostics.</summary>
public sealed class Logger
{
    private readonly object _gate = new();
    private readonly Queue<string> _recent = new();

    public event Action<string>? LineWritten;

    public void Write(string message)
    {
        var line = $"{DateTime.Now:HH:mm:ss}  {message}";
        lock (_gate)
        {
            _recent.Enqueue(line);
            while (_recent.Count > 500)
            {
                _recent.Dequeue();
            }
        }

        try
        {
            LineWritten?.Invoke(line);
        }
        catch
        {
            // Logging must never take down the tunnel.
        }
    }

    public string Snapshot(int count = 200)
    {
        lock (_gate)
        {
            var lines = _recent.ToArray();
            if (lines.Length <= count)
            {
                return string.Join(Environment.NewLine, lines);
            }

            var builder = new StringBuilder();
            for (var i = lines.Length - count; i < lines.Length; i++)
            {
                builder.AppendLine(lines[i]);
            }
            return builder.ToString().TrimEnd();
        }
    }
}
