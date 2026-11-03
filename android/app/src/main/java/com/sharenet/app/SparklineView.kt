package com.sharenet.app

import android.content.Context
import android.content.res.TypedArray
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.LinearGradient
import android.graphics.Paint
import android.graphics.Path
import android.graphics.Shader
import android.util.AttributeSet
import android.view.View
import kotlin.math.max

/**
 * Compact live throughput chart: smoothed line, gradient area fill, hairline
 * grid and a glowing head marker on the newest sample.
 */
class SparklineView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
    defStyleAttr: Int = 0
) : View(context, attrs, defStyleAttr) {

    private val capacity = 48
    private val values = FloatArray(capacity)
    private var count = 0
    private var next = 0
    private var ceiling = 1f

    private var lineColor: Int = 0
    private var glowColor: Int = 0

    private val linePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeWidth = dp(1.7f)
        strokeCap = Paint.Cap.ROUND
        strokeJoin = Paint.Join.ROUND
    }

    private val haloPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeWidth = dp(6f)
        strokeCap = Paint.Cap.ROUND
        strokeJoin = Paint.Join.ROUND
    }

    private val fillPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.FILL
    }

    private val dotPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }

    private val dotHaloPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }

    private val gridPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeWidth = 1f
        color = Color.argb(16, 255, 255, 255)
    }

    private val baselinePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeWidth = 1f
        color = Color.argb(34, 255, 255, 255)
    }

    private val hintPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.FILL
        color = Color.argb(96, 255, 255, 255)
        textAlign = Paint.Align.CENTER
    }

    private val hintText: String = context.getString(R.string.waiting_for_traffic)

    private val linePath = Path()
    private val fillPath = Path()

    private var shaderHeight = -1f

    init {
        if (attrs != null) {
            val a: TypedArray = context.obtainStyledAttributes(attrs, R.styleable.SparklineView)
            lineColor = a.getColor(R.styleable.SparklineView_sparkColor, 0)
            glowColor = a.getColor(R.styleable.SparklineView_sparkGlowColor, 0)
            a.recycle()
        }
        if (lineColor == 0) lineColor = context.getColor(R.color.spark_download)
        if (glowColor == 0) glowColor = lineColor
    }

    fun push(value: Double) {
        values[next] = if (value.isFinite() && value > 0) value.toFloat() else 0f
        next = (next + 1) % capacity
        if (count < capacity) count++
        invalidate()
    }

    fun reset() {
        values.fill(0f)
        count = 0
        next = 0
        ceiling = 1f
        invalidate()
    }

    private fun dp(value: Float): Float = value * resources.displayMetrics.density

    private fun withAlpha(color: Int, alpha: Int): Int =
        Color.argb(alpha, Color.red(color), Color.green(color), Color.blue(color))

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val w = width.toFloat()
        val h = height.toFloat()
        if (w < 2f || h < 2f) return

        drawGrid(canvas, w, h)
        if (count < 2) {
            drawHint(canvas, w, h)
            return
        }

        var peak = 0f
        for (i in 0 until count) peak = max(peak, values[i])
        if (peak < 0.5f) {
            drawHint(canvas, w, h)
            return
        }

        // Slow ceiling decay keeps the curve from rescaling on every burst.
        ceiling = max(peak * 1.15f, ceiling * 0.96f)
        if (ceiling < 1f) ceiling = 1f

        val rightInset = dp(4f)
        val plotWidth = w - rightInset
        val step = plotWidth / (count - 1)
        val bottom = h - 2f
        val top = dp(3f)
        val start = (next - count + capacity) % capacity

        linePath.reset()
        fillPath.reset()

        var previousX = 0f
        var previousY = bottom
        var lastX = 0f
        var lastY = bottom

        for (i in 0 until count) {
            val value = values[(start + i) % capacity]
            val normalised = (value / ceiling).coerceIn(0f, 1f)
            val x = i * step
            val y = bottom - normalised * (bottom - top)

            if (i == 0) {
                linePath.moveTo(x, y)
                fillPath.moveTo(x, bottom)
                fillPath.lineTo(x, y)
            } else {
                val tension = step * 0.42f
                linePath.cubicTo(previousX + tension, previousY, x - tension, y, x, y)
                fillPath.cubicTo(previousX + tension, previousY, x - tension, y, x, y)
            }

            previousX = x
            previousY = y
            lastX = x
            lastY = y
        }

        fillPath.lineTo(lastX, bottom)
        fillPath.close()

        if (shaderHeight != h) {
            fillPaint.shader = LinearGradient(
                0f, 0f, 0f, h,
                intArrayOf(withAlpha(lineColor, 68), withAlpha(lineColor, 14), Color.TRANSPARENT),
                floatArrayOf(0f, 0.55f, 1f),
                Shader.TileMode.CLAMP
            )
            shaderHeight = h
        }

        canvas.drawPath(fillPath, fillPaint)

        haloPaint.color = withAlpha(glowColor, 38)
        canvas.drawPath(linePath, haloPaint)

        linePaint.color = lineColor
        canvas.drawPath(linePath, linePaint)

        dotHaloPaint.color = withAlpha(lineColor, 52)
        canvas.drawCircle(lastX, lastY, dp(5.4f), dotHaloPaint)
        dotPaint.color = lineColor
        canvas.drawCircle(lastX, lastY, dp(2.3f), dotPaint)
    }

    private fun drawHint(canvas: Canvas, w: Float, h: Float) {
        hintPaint.textSize = dp(10f)
        val metrics = hintPaint.fontMetrics
        val baseline = h / 2f - (metrics.ascent + metrics.descent) / 2f
        canvas.drawText(hintText, w / 2f, baseline, hintPaint)
    }

    private fun drawGrid(canvas: Canvas, w: Float, h: Float) {
        for (i in 1..3) {
            val y = h * i / 4f
            canvas.drawLine(0f, y, w, y, gridPaint)
        }
        canvas.drawLine(0f, h - 1f, w, h - 1f, baselinePaint)
    }

    companion object {
        @JvmStatic
        fun setLineColor(view: SparklineView, color: Int) {
            view.lineColor = color
            view.shaderHeight = -1f
            view.invalidate()
        }
    }
}
