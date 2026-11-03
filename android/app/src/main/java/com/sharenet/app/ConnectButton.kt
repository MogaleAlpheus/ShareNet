package com.sharenet.app

import android.animation.ValueAnimator
import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.LinearGradient
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.RadialGradient
import android.graphics.SweepGradient
import android.graphics.Shader
import android.util.AttributeSet
import android.view.HapticFeedbackConstants
import android.view.View
import android.view.animation.AccelerateDecelerateInterpolator
import android.view.animation.LinearInterpolator
import kotlin.math.min

/**
 * Circular power button: state ring around a raised face.
 *
 * States: idle -> connecting (rotating arc) -> sharing (gradient ring + glow)
 * -> error. Everything is drawn inside the view bounds so nothing is clipped:
 * the view reserves [pad] around the geometry.
 */
class ConnectButton @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null
) : View(context, attrs) {

    companion object {
        const val STATE_IDLE = 0
        const val STATE_CONNECTING = 1
        const val STATE_SHARING = 2
        const val STATE_ERROR = 3

        private const val RING_WIDTH_DP = 5f
        private const val PAD_DP = 12f
    }

    private var state = STATE_IDLE

    // Palette
    private val tealTop = 0xFF14D9B4.toInt()
    private val blueBottom = 0xFF5B8CFF.toInt()
    private val warning = 0xFFF2B24C.toInt()
    private val error = 0xFFFF6E6E.toInt()
    private val track = 0xFF2E353F.toInt()
    private val trackIdle = 0xFF2A313B.toInt()
    private val iconIdle = 0xFF7FE3CC.toInt()

    private val trackPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE }
    private val ringPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeCap = Paint.Cap.ROUND
    }
    private val glowPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeCap = Paint.Cap.ROUND
        strokeJoin = Paint.Join.ROUND
    }
    private val facePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }
    private val faceEdgePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeWidth = 1f
    }
    private val iconPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeCap = Paint.Cap.ROUND
        strokeJoin = Paint.Join.ROUND
    }

    private val ambientPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }

    private val wellPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE }

    private val innerPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.FILL }

    /** Hairline rim light inside the plate: the cue that the disc is domed. */
    private val rimPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeCap = Paint.Cap.ROUND
        strokeWidth = dp(1.1f)
        color = Color.argb(18, 255, 255, 255)
    }

    private val specPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        style = Paint.Style.STROKE
        strokeWidth = dp(1.2f)
        strokeCap = Paint.Cap.ROUND
    }

    private var faceShader: RadialGradient? = null
    private var faceEdgeShader: LinearGradient? = null
    private var ambientShader: RadialGradient? = null
    private var innerShader: LinearGradient? = null
    private var trackShader: LinearGradient? = null
    private var sweepShader: SweepGradient? = null

    /** soft top-lit highlight on the bezel: fades to nothing at both ends */
    private var specShader: SweepGradient? = null
    private var sweepWidth = 0f

    /** 0..1 pulse used to breathe the glow while connecting. */
    private var pulse = 1f

    /** Degrees of arc drawn in the connecting state's rotating sweep. */
    private var sweepRotation = 0f
    private var sweepSpan = 100f

    private var pulseAnimator: ValueAnimator? = null
    private var spinAnimator: ValueAnimator? = null

    private fun dp(v: Float): Float = v * resources.displayMetrics.density

    fun setState(value: Int) {
        if (state == value) return
        state = value
        pulseAnimator?.cancel()
        spinAnimator?.cancel()
        pulseAnimator = null
        spinAnimator = null

        when (value) {
            STATE_CONNECTING -> {
                pulseAnimator = ValueAnimator.ofFloat(0.45f, 1f).apply {
                    duration = 850
                    repeatCount = ValueAnimator.INFINITE
                    repeatMode = ValueAnimator.REVERSE
                    interpolator = AccelerateDecelerateInterpolator()
                    addUpdateListener {
                        pulse = it.animatedValue as Float
                        invalidate()
                    }
                    start()
                }
                spinAnimator = ValueAnimator.ofFloat(0f, 360f).apply {
                    duration = 1400
                    repeatCount = ValueAnimator.INFINITE
                    interpolator = LinearInterpolator()
                    addUpdateListener {
                        sweepRotation = it.animatedValue as Float
                        invalidate()
                    }
                    start()
                }
            }
            else -> {
                pulse = 1f
                sweepRotation = 0f
            }
        }
        invalidate()
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val cx = width / 2f
        val cy = height / 2f
        if (cx < dp(PAD_DP)) return
        val radius = min(cx, cy) - dp(PAD_DP)
        val ringW = dp(RING_WIDTH_DP)
        val faceR = radius - ringW - dp(3f)

        // ---- Face (raised plate): radial gradient + top edge highlight ----
        if (faceShader == null || sweepWidth != width.toFloat()) {
            faceShader = RadialGradient(
                cx, cy - faceR * 0.2f, faceR * 1.15f,
                intArrayOf(0xFF252C36.toInt(), 0xFF141920.toInt()),
                floatArrayOf(0f, 1f), Shader.TileMode.CLAMP)
            faceEdgeShader = LinearGradient(
                cx, cy - faceR, cx, cy + faceR,
                intArrayOf(Color.argb(60, 255, 255, 225), Color.argb(0, 255, 255, 255)),
                null,
                Shader.TileMode.CLAMP)
            facePaint.shader = faceShader
            faceEdgePaint.shader = faceEdgeShader
            ambientShader = RadialGradient(
                cx, cy + dp(3f), radius + dp(9f),
                intArrayOf(Color.argb(72, 0, 0, 0), Color.TRANSPARENT),
                floatArrayOf(0.72f, 1f), Shader.TileMode.CLAMP)
            ambientPaint.shader = ambientShader
            trackShader = LinearGradient(
                cx, cy - radius, cx, cy + radius,
                intArrayOf(0xFF4A5563.toInt(), trackIdle, 0xFF15191F.toInt()),
                floatArrayOf(0f, 0.5f, 1f), Shader.TileMode.CLAMP)
            // Rim occlusion: darker where the plate curves away from the light
            // at the top and where it meets the bezel at the bottom.
            innerShader = LinearGradient(
                cx, cy - faceR, cx, cy + faceR,
                intArrayOf(
                    Color.argb(115, 0, 0, 0),
                    Color.TRANSPARENT,
                    Color.TRANSPARENT,
                    Color.argb(110, 0, 0, 0)),
                floatArrayOf(0f, 0.32f, 0.64f, 1f), Shader.TileMode.CLAMP)
            // Sweep gradient: peak at the top (270deg = 0.75 of the sweep),
            // transparent on both sides so the highlight has no hard ends.
            specShader = SweepGradient(
                cx, cy,
                intArrayOf(
                    Color.TRANSPARENT,
                    Color.TRANSPARENT,
                    Color.argb(78, 255, 255, 255),
                    Color.TRANSPARENT,
                    Color.TRANSPARENT),
                floatArrayOf(0f, 0.6f, 0.75f, 0.9f, 1f))
            sweepWidth = width.toFloat()
        }

        // ---- Ambient depth under the plate ----
        canvas.drawCircle(cx, cy + dp(3f), radius + dp(9f), ambientPaint)

        // ---- Recessed well: the bezel sits in the card instead of on top of it ----
        wellPaint.color = Color.argb(60, 0, 0, 0)
        wellPaint.strokeWidth = ringW + dp(4f)
        canvas.drawCircle(cx, cy, radius - ringW / 2f + dp(1f), wellPaint)

        canvas.drawCircle(cx, cy, faceR, facePaint)

        // ---- Rim occlusion + inner rim light: the plate reads as domed ----
        innerPaint.shader = innerShader
        canvas.drawCircle(cx, cy, faceR, innerPaint)
        canvas.drawArc(
            cx - faceR + dp(2.5f), cy - faceR + dp(2.5f),
            cx + faceR - dp(2.5f), cy + faceR - dp(2.5f),
            208f, 124f, false, rimPaint
        )

        // ---- Track ring: gradient bezel with a lit top edge ----
        trackPaint.shader = trackShader
        trackPaint.strokeWidth = ringW
        canvas.drawCircle(cx, cy, radius - ringW / 2f, trackPaint)
        trackPaint.shader = null

        val bezelRadius = radius - ringW / 2f
        specPaint.shader = specShader
        canvas.drawCircle(cx, cy, bezelRadius, specPaint)
        specPaint.shader = null

        val stateColor: Int
        val iconColor: Int
        when (state) {
            STATE_SHARING -> {
                stateColor = tealTop
                iconColor = tealTop
            }
            STATE_CONNECTING -> {
                stateColor = warning
                iconColor = warning
            }
            STATE_ERROR -> {
                stateColor = error
                iconColor = error
            }
            else -> {
                stateColor = track
                iconColor = iconIdle
            }
        }

        // ---- State ring on top of the track ----
        when (state) {
            STATE_SHARING -> {
                if (sweepShader == null || sweepWidth != width.toFloat()) {
                    val m = Matrix()
                    m.setRotate(-115f, cx, cy)  // teal at the top, blue at the bottom
                    sweepShader = SweepGradient(
                        cx, cy,
                        intArrayOf(tealTop, blueBottom, tealTop),
                        floatArrayOf(0.0f, 0.35f, 1.0f))
                    sweepShader!!.setLocalMatrix(m)
                }
                ringPaint.shader = sweepShader
                ringPaint.color = Color.WHITE
                ringPaint.strokeWidth = ringW

                // Fake glow: wider, translucent strokes beneath the crisp ring.
                glowPaint.shader = null
                glowPaint.color = Color.argb((46 * pulse).toInt(), 20, 217, 180)
                glowPaint.strokeWidth = ringW + dp(7f)
                canvas.drawCircle(cx, cy, radius - ringW / 2f, glowPaint)
                glowPaint.color = Color.argb((22 * pulse).toInt(), 20, 217, 180)
                glowPaint.strokeWidth = ringW + dp(15f)
                canvas.drawCircle(cx, cy, radius - ringW / 2f - dp(3f), glowPaint)

                canvas.drawCircle(cx, cy, radius - ringW / 2f, ringPaint)
                ringPaint.shader = null
            }
            STATE_CONNECTING -> {
                itemRing(canvas, cx, cy, radius, sweepSpan, sweepRotation, stateColor, ringW, 1f)
            }
            else -> {
                if (state == STATE_ERROR) {
                    // Error: full error-colored ring + light pulse to draw the eye.
                    ringPaint.shader = null
                    ringPaint.color = stateColor
                    ringPaint.strokeWidth = ringW
                    glowPaint.shader = null
                    glowPaint.color = Color.argb((36 * pulse).toInt(), 255, 110, 110)
                    glowPaint.strokeWidth = ringW + dp(7f)
                    canvas.drawCircle(cx, cy, radius - ringW / 2f, glowPaint)
                    canvas.drawCircle(cx, cy, radius - ringW / 2f, ringPaint)
                } else {
                    // Idle: track is already drawn; nothing extra.
                }
            }
        }

        // ---- Inner hairline highlight at the face edge ----
        canvas.drawCircle(cx, cy, faceR - 0.5f, faceEdgePaint)

        // ---- Power glyph: broken circle with a vertical stem ----
        val iconR = faceR * 0.40f
        iconPaint.color = iconColor
        iconPaint.strokeWidth = dp(3.8f)
        // Gap of 70° centered at the top (270°). Android angles: 0° = 3 o'clock.
        canvas.drawArc(
            cx - iconR, cy - iconR, cx + iconR, cy + iconR,
            305f, 290f, false, iconPaint
        )
        canvas.drawLine(
            cx, cy + dp(2f),   // small tail below center so the stem reads at eye level
            cx, cy - iconR - dp(1f),
            iconPaint
        )
    }

    private fun itemRing(
        canvas: Canvas,
        cx: Float,
        cy: Float,
        radius: Float,
        span: Float,
        rotation: Float,
        color: Int,
        width: Float,
        alpha: Float
    ) {
        val r = radius - width / 2f
        glowPaint.shader = null
        glowPaint.color = Color.argb((alpha * 26).toInt(), Color.red(color), Color.green(color), Color.blue(color))
        glowPaint.strokeWidth = width + dp(6f)
        canvas.drawArc(cx - r, cy - r, cx + r, cy + r, rotation - 90f, span, false, glowPaint)

        ringPaint.shader = null
        ringPaint.color = color
        ringPaint.strokeWidth = width
        ringPaint.alpha = (alpha * 255).toInt()
        canvas.drawArc(cx - r, cy - r, cx + r, cy + r, rotation - 90f, span, false, ringPaint)
        ringPaint.alpha = 255
    }

    override fun onTouchEvent(event: android.view.MotionEvent): Boolean {
        if (isEnabled) {
            when (event.actionMasked) {
                android.view.MotionEvent.ACTION_DOWN -> {
                    animate().scaleX(0.96f).scaleY(0.96f)
                        .setDuration(70).setInterpolator(AccelerateDecelerateInterpolator()).start()
                    performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
                }
                android.view.MotionEvent.ACTION_UP,
                android.view.MotionEvent.ACTION_CANCEL -> {
                    animate().scaleX(1f).scaleY(1f)
                        .setDuration(140).setInterpolator(AccelerateDecelerateInterpolator()).start()
                }
            }
        }
        return super.onTouchEvent(event)
    }

    override fun onDetachedFromWindow() {
        pulseAnimator?.cancel()
        spinAnimator?.cancel()
        pulseAnimator = null
        spinAnimator = null
        super.onDetachedFromWindow()
    }
}
