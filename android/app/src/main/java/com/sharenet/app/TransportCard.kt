package com.sharenet.app

import android.content.Context
import android.util.AttributeSet
import android.widget.Checkable
import android.widget.LinearLayout

/**
 * Card that behaves like a radio button for state purposes: it is a real
 * ViewGroup (unlike RadioButton) and draws state_checked so state-list
 * drawables (background and children) react to selection.
 */
class TransportCard @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
    defStyleAttr: Int = 0
) : LinearLayout(context, attrs, defStyleAttr), Checkable {

    private var checked = false

    init {
        val a = context.obtainStyledAttributes(attrs, intArrayOf(android.R.attr.checked))
        if (a.hasValue(0)) {
            isChecked = a.getBoolean(0, false)
        }
        a.recycle()
    }

    override fun isChecked(): Boolean = checked

    override fun setChecked(checked: Boolean) {
        if (this.checked == checked) return
        this.checked = checked
        refreshDrawableState()
    }

    override fun toggle() {
        isChecked = !isChecked
    }

    override fun onCreateDrawableState(extraSpace: Int): IntArray {
        val state = super.onCreateDrawableState(extraSpace + 1)
        if (checked) {
            mergeDrawableStates(state, intArrayOf(android.R.attr.state_checked))
        }
        return state
    }
}
