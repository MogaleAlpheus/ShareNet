# ShareNet Design System v2.0

## Brand Identity

### Logo Concept
**Mark**: A clean "S" formed by two intersecting arcs — one representing the phone (shorter arc), one the PC (longer arc) — meeting at a connection point. Minimal, geometric, scalable.

**Logotype**: "ShareNet" in **Space Grotesk SemiBold** (or **Inter SemiBold** fallback), tracking +0.5.

**Color**: Primary gradient `#00D4AA` → `#0099FF` (teal to blue) — modern, tech-forward, trustworthy.

---

## Color Palette

### Light Theme (Primary)
| Token | Hex | Usage |
|-------|-----|-------|
| `bg` | `#F8FAFC` | Page background |
| `bg_elevated` | `#FFFFFF` | Cards, surfaces |
| `surface` | `#F1F5F9` | Secondary surfaces |
| `border` | `#E2E8F0` | Dividers, borders |
| `border_strong` | `#CBD5E1` | Focus rings, active borders |
| `text` | `#0F172A` | Primary text |
| `text_secondary` | `#475569` | Secondary text |
| `text_muted` | `#94A3B8` | Placeholders, captions |
| `brand` | `#00B894` | Primary actions, links |
| `brand_strong` | `#00997A` | Hover/pressed states |
| `brand_soft` | `#E6FFF8` | Backgrounds, badges |
| `brand_glow` | `#00D4AA` | Glows, focus |
| `accent` | `#0099FF` | Secondary actions, info |
| `accent_soft` | `#E8F4FF` | Info backgrounds |
| `success` | `#00C853` | Connected, positive |
| `success_soft` | `#E8FFF0` | Success backgrounds |
| `warning` | `#FFB300` | Pending, attention |
| `warning_soft` | `#FFF8E1` | Warning backgrounds |
| `error` | `#FF3B30` | Errors, disconnected |
| `error_soft` | `#FFEBEE` | Error backgrounds |

### Dark Theme (Default - matches current vibe)
| Token | Hex | Usage |
|-------|-----|-------|
| `bg` | `#050810` | Page background |
| `bg_elevated` | `#0B1220` | Cards, surfaces |
| `surface` | `#0E1628` | Secondary surfaces |
| `border` | `#1E2D45` | Dividers, borders |
| `border_strong` | `#2A3F5E` | Focus rings |
| `text` | `#F0F4FA` | Primary text |
| `text_secondary` | `#B8C5D8` | Secondary text |
| `text_muted` | `#7A8CA8` | Placeholders |
| `brand` | `#00D4AA` | Primary actions |
| `brand_strong` | `#00E8BB` | Hover/pressed |
| `brand_soft` | `#0A2E26` | Backgrounds |
| `brand_glow` | `#00D4AA` | Glows |
| `accent` | `#4DA3FF` | Secondary |
| `accent_soft` | `#1A2A44` | Info backgrounds |
| `success` | `#34D399` | Connected |
| `success_soft` | `#103024` | Success bg |
| `warning` | `#FBBF24` | Pending |
| `warning_soft` | `#302810` | Warning bg |
| `error` | `#F87171` | Errors |
| `error_soft` | `#30181C` | Error bg |

### Gradient System
- **Primary**: `linear(135deg, #00D4AA 0%, #0099FF 100%)`
- **Hero**: `linear(135deg, #0A1628 0%, #050810 100%)`
- **Card**: `linear(180deg, #0E1628 0%, #0B1220 100%)`
- **Glow**: `radial(circle at center, #00D4AA22 0%, transparent 70%)`

---

## Typography

### Font Stack
- **Display/Headlines**: Space Grotesk (fallback: Inter, Segoe UI Variable)
- **UI/Body**: Inter (fallback: Segoe UI Variable, system-ui)
- **Mono**: JetBrains Mono (fallback: Cascadia Mono, Consolas)

### Scale (Mobile / Desktop)
| Role | Mobile | Desktop | Weight | Line Height |
|------|--------|---------|--------|-------------|
| Display Large | 44 / 52 | 56 / 64 | 600 | 1.1 |
| Display Medium | 36 / 44 | 44 / 52 | 600 | 1.15 |
| Display Small | 28 / 36 | 32 / 40 | 600 | 1.2 |
| Headline Large | 24 / 32 | 28 / 36 | 600 | 1.25 |
| Headline Medium | 20 / 28 | 22 / 30 | 600 | 1.3 |
| Headline Small | 18 / 26 | 20 / 28 | 600 | 1.35 |
| Title Large | 16 / 24 | 18 / 26 | 600 | 1.4 |
| Title Medium | 14 / 22 | 15 / 24 | 500 | 1.45 |
| Title Small | 13 / 20 | 14 / 22 | 500 | 1.5 |
| Body Large | 16 / 24 | 17 / 26 | 400 | 1.5 |
| Body Medium | 14 / 22 | 15 / 24 | 400 | 1.5 |
| Body Small | 12 / 18 | 13 / 20 | 400 | 1.5 |
| Label Large | 13 / 20 | 14 / 22 | 500 | 1.4 |
| Label Medium | 12 / 18 | 13 / 20 | 500 | 1.45 |
| Label Small | 11 / 16 | 12 / 18 | 500 | 1.5 |
| Caption | 11 / 16 | 12 / 18 | 400 | 1.5 |
| Overline | 10 / 16 | 11 / 16 | 500 | 1.5 | (uppercase, tracking +1.5)

---

## Spacing System
Base unit: **4px**

| Token | Value |
|-------|-------|
| space-1 | 4px |
| space-2 | 8px |
| space-3 | 12px |
| space-4 | 16px |
| space-5 | 20px |
| space-6 | 24px |
| space-8 | 32px |
| space-10 | 40px |
| space-12 | 48px |
| space-16 | 64px |

---

## Border Radius
| Token | Value |
|-------|-------|
| radius-xs | 4px |
| radius-sm | 8px |
| radius-md | 12px |
| radius-lg | 16px |
| radius-xl | 20px |
| radius-2xl | 24px |
| radius-full | 9999px |

---

## Shadows (Dark Theme)
| Token | Value |
|-------|-------|
| shadow-xs | `0 1px 2px rgba(0,0,0,0.3)` |
| shadow-sm | `0 2px 4px rgba(0,0,0,0.35)` |
| shadow-md | `0 4px 12px rgba(0,0,0,0.4)` |
| shadow-lg | `0 8px 24px rgba(0,0,0,0.45)` |
| shadow-xl | `0 16px 48px rgba(0,0,0,0.5)` |
| shadow-glow | `0 0 24px rgba(0,212,170,0.25)` |
| shadow-inner | `inset 0 1px 2px rgba(0,0,0,0.3)` |

---

## Motion
| Token | Duration | Easing |
|-------|----------|--------|
| fast | 120ms | ease-out |
| normal | 200ms | ease-out |
| slow | 300ms | ease-out |
| spring | 400ms | cubic-bezier(0.34, 1.56, 0.64, 1) |

---

## Iconography
- **Style**: Outline, 2px stroke, 24×24px base
- **Corners**: Rounded (radius 2)
- **Ends**: Round caps, round joins
- **Weight**: Consistent 2px across all icons

---

## Component Patterns

### Buttons
| Variant | Background | Text | Border | States |
|---------|------------|------|--------|--------|
| Primary | `brand` gradient | white | none | hover: 0.9 opacity, press: 0.75 |
| Secondary | `surface` | `text` | `border_strong` | hover: `border`, press: opacity 0.8 |
| Ghost | transparent | `brand` | none | hover: `brand_soft` bg |
| Destructive | `error` | white | none | hover: 0.9, press: 0.75 |

### Inputs
- Height: 48px (mobile), 44px (desktop)
- Padding: 16px horizontal, 12px vertical
- Border: `border`, 1px → focus: `brand`, 2px
- Background: `bg_elevated`
- Placeholder: `text_muted`
- Error: `error` border, `error_soft` bg

### Cards
- Background: `bg_elevated`
- Border: `border`, 1px
- Radius: `radius-xl` (20px)
- Padding: 24px
- Shadow: `shadow-md`

### Chips/Badges
- Padding: 6px 12px
- Radius: `radius-full`
- Font: Label Medium
- Variants: Default (`surface`/`text_secondary`), Brand (`brand_soft`/`brand`), Success, Warning, Error

---

## Android Implementation Notes
- Use **Material 3** components via `com.google.android.material`
- Enable dynamic color (Material You) on Android 12+
- Dark theme default, light theme optional
- Use `MaterialCardView`, `MaterialButton`, `TextInputLayout`
- Sparkline: Custom `View` with `Path` drawing, 60fps

---

## Windows Implementation Notes
- Target .NET 8, WPF with `Microsoft.Web.WebView2` for any web views
- Use **Fluent 2** aesthetic: Acrylic/Mica backgrounds, rounded corners
- Custom control templates for all standard controls
- `WindowChrome` for custom title bar (optional)
- Sparkline: `Polyline` in `Canvas`, updated via `DispatcherTimer`

---

## Logo Assets Needed
| Asset | Size | Format |
|-------|------|--------|
| App Icon (Android) | 512×512 | PNG (adaptive icon layers) |
| App Icon (Windows) | 256×256 | ICO (multi-size) |
| Notification Icon | 24×24 | PNG (white, transparent bg) |
| Logo Mark Only | 512×512 | SVG + PNG |
| Logo Full (Mark + Type) | 1024×256 | SVG + PNG |
| Splash Screen | 2048×2048 | PNG |

---

## Implementation Priority
1. **Logo & Color System** (shared)
2. **Android**: Colors, Theme, MainActivity layout, Sparkline View
3. **Windows**: App.xaml resources, MainWindow.xaml, Custom controls
4. **Shared**: Splash screens, notification icons