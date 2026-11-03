using System.Globalization;
using System.Windows;
using System.Windows.Media;

namespace ShareNet;

/// <summary>
/// A right-anchored live throughput chart: smoothed line, gradient area fill,
/// hairline grid, peak scale label and a glowing head marker at the newest sample.
/// </summary>
public sealed class Sparkline : FrameworkElement
{
    private const int Capacity = 60;

    private readonly double[] _values = new double[Capacity];
    private readonly SolidColorBrush _gridBrush = Frozen(Color.FromArgb(18, 255, 255, 255));
    private readonly SolidColorBrush _baselineBrush = Frozen(Color.FromArgb(40, 255, 255, 255));
    private readonly SolidColorBrush _scaleBrush = Frozen(Color.FromRgb(0x5D, 0x67, 0x76));
    private readonly SolidColorBrush _hintBrush = Frozen(Color.FromRgb(0x53, 0x5C, 0x6A));
    private readonly Typeface _typeface = new(
        new FontFamily("Segoe UI Variable Text, Segoe UI"),
        FontStyles.Normal,
        FontWeights.Normal,
        FontStretches.Normal);

    private int _count;
    private int _next;
    private double _ceiling = 1;

    public Brush? LineBrush { get; set; }

    public Brush? FillBrush { get; set; }

    public void Push(double value)
    {
        _values[_next] = double.IsFinite(value) && value > 0 ? value : 0;
        _next = (_next + 1) % Capacity;
        if (_count < Capacity)
        {
            _count++;
        }

        InvalidateVisual();
    }

    public void Reset()
    {
        Array.Clear(_values);
        _count = 0;
        _next = 0;
        _ceiling = 1;
        InvalidateVisual();
    }

    protected override void OnRender(DrawingContext dc)
    {
        var width = ActualWidth;
        var height = ActualHeight;
        if (width <= 4 || height <= 4)
        {
            return;
        }

        var pixelsPerDip = VisualTreeHelper.GetDpi(this).PixelsPerDip;
        DrawGrid(dc, width, height);
        DrawBaseline(dc, width, height);

        if (_count < 2)
        {
            DrawHint(dc, width, height, pixelsPerDip);
            return;
        }

        var accent = (LineBrush as SolidColorBrush)?.Color ?? Color.FromRgb(0x5B, 0x8C, 0xFF);

        // Track the peak with a slow decay so the chart does not rescale on every burst.
        var peak = 1.0;
        for (var i = 0; i < _count; i++)
        {
            peak = Math.Max(peak, _values[i]);
        }

        var target = Math.Max(peak * 1.12, 1);
        _ceiling = Math.Max(target, _ceiling * 0.96);

        // Reserve room on the right so the head marker and its glow are never clipped.
        const double headInset = 7;
        var plotWidth = Math.Max(1, width - headInset);
        var step = plotWidth / (Capacity - 1);
        var start = (_next - _count + Capacity) % Capacity;
        var baseline = height - 3;
        var bottom = height;

        // Keep the curve below the scale label so the two never collide.
        var plotHeight = Math.Max(1, baseline - 14);

        // Grow left to right while history builds up, then scroll once the buffer is full.
        var points = new Point[_count];
        for (var i = 0; i < _count; i++)
        {
            var raw = _values[(start + i) % Capacity];
            var normalised = Math.Clamp(raw / _ceiling, 0, 1);
            var x = i * step;
            var y = baseline - normalised * plotHeight;
            points[i] = new Point(x, y);
        }

        DrawScale(dc, _ceiling, pixelsPerDip);

        var line = BuildSmoothGeometry(points, step);
        var area = CloseToBaseline(points, bottom, step);

        var fill = FillBrush ?? new LinearGradientBrush(
            Color.FromArgb(62, accent.R, accent.G, accent.B),
            Color.FromArgb(0, accent.R, accent.G, accent.B),
            new Point(0, 0),
            new Point(0, 1));
        fill.Freeze();

        var stroke = new Pen(Frozen(accent), 1.7)
        {
            LineJoin = PenLineJoin.Round,
            StartLineCap = PenLineCap.Round,
            EndLineCap = PenLineCap.Round
        };
        stroke.Freeze();

        dc.DrawGeometry(fill, null, area);
        dc.DrawGeometry(null, stroke, line);

        DrawHead(dc, points[^1], accent);
    }

    private void DrawGrid(DrawingContext dc, double width, double height)
    {
        var pen = new Pen(_gridBrush, 1);
        pen.Freeze();
        var dpi = VisualTreeHelper.GetDpi(this).DpiScaleY;

        for (var i = 1; i <= 3; i++)
        {
            var y = SnapLine(height * i / 4, dpi);
            dc.DrawLine(pen, new Point(0, y), new Point(width, y));
        }
    }

    private void DrawBaseline(DrawingContext dc, double width, double height)
    {
        var pen = new Pen(_baselineBrush, 1);
        pen.Freeze();
        var y = SnapLine(height, VisualTreeHelper.GetDpi(this).DpiScaleY);
        dc.DrawLine(pen, new Point(0, y), new Point(width, y));
    }

    /// <summary>Centres a 1px line on a device pixel so hairlines stay sharp at any scale.</summary>
    private static double SnapLine(double dip, double dpiScale)
    {
        return (Math.Round(dip * dpiScale - 0.5) + 0.5) / dpiScale;
    }

    private void DrawScale(DrawingContext dc, double ceiling, double pixelsPerDip)
    {
        var text = new FormattedText(
            "peak " + FormatRate(ceiling),
            CultureInfo.InvariantCulture,
            FlowDirection.LeftToRight,
            _typeface,
            9.5,
            _scaleBrush,
            pixelsPerDip);

        dc.DrawText(text, new Point(0, 1));
    }

    private void DrawHint(DrawingContext dc, double width, double height, double pixelsPerDip)
    {
        var text = new FormattedText(
            "Waiting for traffic",
            CultureInfo.InvariantCulture,
            FlowDirection.LeftToRight,
            _typeface,
            10.5,
            _hintBrush,
            pixelsPerDip);

        dc.DrawText(text, new Point((width - text.Width) / 2, (height - text.Height) / 2));
    }

    private static void DrawHead(DrawingContext dc, Point head, Color accent)
    {
        dc.DrawEllipse(Frozen(Color.FromArgb(44, accent.R, accent.G, accent.B)), null, head, 6.5, 6.5);
        dc.DrawEllipse(Frozen(Color.FromArgb(88, accent.R, accent.G, accent.B)), null, head, 3.6, 3.6);
        dc.DrawEllipse(Frozen(accent), null, head, 2.1, 2.1);
    }

    private static StreamGeometry BuildSmoothGeometry(Point[] points, double step)
    {
        var geometry = new StreamGeometry();
        using (var context = geometry.Open())
        {
            context.BeginFigure(points[0], isFilled: false, isClosed: false);
            AddSmoothSegments(context, points, step);
        }

        geometry.Freeze();
        return geometry;
    }

    private static StreamGeometry CloseToBaseline(Point[] points, double baseline, double step)
    {
        var geometry = new StreamGeometry();
        using (var context = geometry.Open())
        {
            context.BeginFigure(new Point(points[0].X, baseline), isFilled: true, isClosed: true);
            context.LineTo(points[0], isStroked: false, isSmoothJoin: false);
            AddSmoothSegments(context, points, step);
            context.LineTo(new Point(points[^1].X, baseline), isStroked: false, isSmoothJoin: false);
        }

        geometry.Freeze();
        return geometry;
    }

    /// <summary>Horizontal-tangent cubic segments give a calm, readable curve.</summary>
    private static void AddSmoothSegments(StreamGeometryContext context, Point[] points, double step)
    {
        var tension = step * 0.36;
        for (var i = 1; i < points.Length; i++)
        {
            var previous = points[i - 1];
            var current = points[i];
            context.BezierTo(
                new Point(previous.X + tension, previous.Y),
                new Point(current.X - tension, current.Y),
                current,
                isStroked: true,
                isSmoothJoin: true);
        }
    }

    private static string FormatRate(double bytesPerSecond)
    {
        if (bytesPerSecond < 1024)
        {
            return bytesPerSecond.ToString("0", CultureInfo.InvariantCulture) + " B/s";
        }

        if (bytesPerSecond < 1024 * 1024)
        {
            return (bytesPerSecond / 1024).ToString("0.#", CultureInfo.InvariantCulture) + " KB/s";
        }

        if (bytesPerSecond < 1024 * 1024 * 1024)
        {
            return (bytesPerSecond / (1024 * 1024)).ToString("0.#", CultureInfo.InvariantCulture) + " MB/s";
        }

        return (bytesPerSecond / (1024 * 1024 * 1024)).ToString("0.0", CultureInfo.InvariantCulture) + " GB/s";
    }

    private static SolidColorBrush Frozen(Color color)
    {
        var brush = new SolidColorBrush(color);
        brush.Freeze();
        return brush;
    }
}
