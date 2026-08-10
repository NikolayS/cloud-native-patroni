This file and the assets beside it are covered by
[`LICENSE`](../../../LICENSE), like the other project-owned files.

# CloudNativePatroni brand guideline

## Purpose

This guideline covers the CloudNativePatroni mark, lockup, colour system,
typography and documentation design. It binds maintainers, contributors and
anyone producing project-owned visual or documentation material.

Owner directive D-1 states:

> Use a completely independent logo, colour system, typography, and
> documentation design. Do not imitate CloudNativePG's visual identity
> and do not create a logo that looks like a modified CNPG logo.

## The independence statement

> CloudNativePatroni is an independent project derived from
> CloudNativePG. It is not affiliated with or endorsed by
> CloudNativePG, CNCF, LF Projects, or the Patroni maintainers.

## The mark

The mark is a masonry arch of three voussoirs. The flanking stones bear against
the keystone's radial sides and hold it at the crown; if that mutual support is
withdrawn, the keystone falls. This represents the leader-lock model, in which
a leader holds write authority only while its peers' agreement holds it there.
An arch also admits exactly one keystone, so a second authority is structurally
impossible. The keystone is drawn in the brand primary and projects one unit
beyond the arch face.

The mark is flat, three closed paths, no gradient, no raster, no font, no
mascot, no enclosing tile.

### Construction

All coordinates are on a 24 × 24 unit grid, expressed directly as the SVG
`viewBox`.

| Quantity | Value |
|---|---|
| Canvas | 24 × 24 units |
| Artwork bounding box | x from 2 to 22, y from 2 to 22 — exactly 20 × 20, centred, 2 units clear on all sides |
| Arch centre C | (12, 13) |
| Outer radius of flanking voussoirs | 10 |
| Inner radius of the arch opening | 6 |
| Outer radius of the keystone | 11 (projects 1 unit past the arch face) |
| Flanking arch band thickness | 4 units |
| Keystone radial depth | 5 units |
| Pier width | 4 units, x from 2 to 6 and from 18 to 22 |
| Pier height | 9 units, y from 13 to 22 |
| Springing line | y = 13 |
| Baseline | y = 22 |
| Left voussoir angular span | 180 degrees to 113.432432 degrees |
| Joint | 113.432432 degrees to 112 degrees (1.432432 degrees; 0.25 units at radius 10) |
| Keystone angular span | 112 degrees to 68 degrees (44 degrees, symmetric about the vertical) |
| Joint | 68 degrees to 66.567568 degrees (1.432432 degrees; 0.25 units at radius 10) |
| Right voussoir angular span | 66.567568 degrees to 0 degrees |
| Corner treatment | none — all corners square, no rounding anywhere |

Angles are measured counter-clockwise from the positive x axis in the ordinary
visual sense, with "up" being up on screen. These endpoint coordinates are
authoritative:

```text
left pier and voussoir:
M2 13A10 10 0 0 1 8.023327 3.824703L9.613996 7.494822A6 6 0 0 0 6 13L6 22L2 22Z

right pier and voussoir:
M22 13A10 10 0 0 0 15.976673 3.824703L14.386004 7.494822A6 6 0 0 1 18 13L18 22L22 22Z

keystone:
M7.879327 2.800978A11 11 0 0 1 16.120673 2.800978L14.247640 7.436897A6 6 0 0 0 9.752360 7.436897Z
```

Each joint is unpainted geometry between two radial edges converging on C. Its
angular width is derived as `2 × asin(0.25 / (2 × 10))`, giving a 0.25-unit
chord at the flanking stones' outer face and a 0.15-unit chord at the inner
face.

### Usage rules

- Minimum size is 20 px; 24 px or larger is preferred. At very small sizes the
  thin joints merge and the mark degrades to a solid arch.
- Clear space on all sides equals the arch band thickness, 4 units on the
  24-unit grid, measured outward from the artwork bounding box.
- Never rotate, shear, outline, add a drop shadow, apply a gradient, place the
  mark inside a rounded tile or badge, recolour the keystone to anything but
  garnet, or recolour the structure to the keystone colour.
- Never combine the mark with the CloudNativePG mark, the Postgres elephant, the
  CNCF logo or the Kubernetes helm.

### How this differs from the CloudNativePG mark

CloudNativePG's mark is an illustrated elephant mascot filled with a
violet-to-navy radial gradient; this mark is three flat geometric paths with no
mascot, no gradient, no shared hue, and no shared silhouette. Nothing in it is
derived from, traced from, or a modification of the CloudNativePG artwork.

## Files

| File | Purpose | When to use it |
|---|---|---|
| [`mark.svg`](mark.svg) | Full-colour standalone mark in basalt and garnet | General use on any verified light or dark host background, at 20 px minimum and preferably 24 px or larger |
| [`mark-mono.svg`](mark-mono.svg) | Single-tone mark inheriting `currentColor` | Inline SVG, print, stencils and downstream favicon generation; it must not be used in an `<img>` element, where it renders black |
| [`lockup.svg`](lockup.svg) | Horizontal mark and outlined CloudNativePatroni wordmark | Use when the project name should accompany the mark; minimum reproduction width is 160 px |
| [`tokens.css`](tokens.css) | Raw colour, typography, structural and theme custom properties | Documentation and other project-owned interfaces that need the brand system without component styles |

## Colour

### The no-blue rule

The palette contains no blue. Postgres, Patroni, CloudNativePG, Crunchy Data and
StackGres are all blue-led identities. Removing blue entirely — from the mark,
the ramps, the semantic colours and the diagram colours — is the single
strongest structural guarantee of independence, and it is a rule, not a
preference. The informational status colour is therefore neutral, not blue.

### Basalt — the neutral ramp

Basalt is warm, with hue 33 to 43 degrees and saturation at or below 20 percent
from basalt-400 downward. Eleven steps satisfy the nine-step minimum.

| Token | Hex | HSL | Contrast vs white | Contrast vs basalt-950 |
|---|---|---|---|---|
| `--cnpatroni-basalt-50` | `#FAF8F4` | 40, 37%, 97% | 1.06 | 17.35 |
| `--cnpatroni-basalt-100` | `#F3EFE8` | 38, 31%, 93% | 1.15 | 16.06 |
| `--cnpatroni-basalt-200` | `#E6DFD3` | 38, 28%, 86% | 1.32 | 13.90 |
| `--cnpatroni-basalt-300` | `#D2C8B7` | 38, 23%, 77% | 1.66 | 11.12 |
| `--cnpatroni-basalt-400` | `#B0A492` | 36, 16%, 63% | 2.45 | 7.51 |
| `--cnpatroni-basalt-500` | `#827868` | 37, 11%, 46% | 4.34 | 4.24 |
| `--cnpatroni-basalt-600` | `#6E6558` | 35, 11%, 39% | 5.73 | 3.21 |
| `--cnpatroni-basalt-700` | `#565043` | 41, 12%, 30% | 8.00 | 2.30 |
| `--cnpatroni-basalt-800` | `#3D3832` | 33, 10%, 22% | 11.60 | 1.59 |
| `--cnpatroni-basalt-900` | `#26231F` | 34, 10%, 14% | 15.64 | 1.18 |
| `--cnpatroni-basalt-950` | `#16140F` | 43, 19%, 7% | 18.40 | 1.00 |

### Garnet — brand primary

Garnet spans hue 333 to 338 degrees.

| Token | Hex | HSL | Contrast vs white | Contrast vs basalt-950 |
|---|---|---|---|---|
| `--cnpatroni-garnet-50` | `#FDF1F6` | 335, 75%, 97% | 1.10 | 16.73 |
| `--cnpatroni-garnet-100` | `#FBE0EC` | 333, 77%, 93% | 1.24 | 14.88 |
| `--cnpatroni-garnet-200` | `#F6C2D9` | 333, 74%, 86% | 1.54 | 11.95 |
| `--cnpatroni-garnet-300` | `#EE97BA` | 336, 72%, 76% | 2.15 | 8.56 |
| `--cnpatroni-garnet-400` | `#E06894` | 338, 66%, 64% | 3.19 | 5.76 |
| `--cnpatroni-garnet-500` | `#CE4478` | 337, 58%, 54% | 4.45 | 4.14 |
| `--cnpatroni-garnet-600` | `#A9265A` | 336, 63%, 41% | 6.75 | 2.73 |
| `--cnpatroni-garnet-700` | `#8A1D49` | 336, 65%, 33% | 8.93 | 2.06 |
| `--cnpatroni-garnet-800` | `#6B1739` | 336, 65%, 25% | 11.62 | 1.58 |
| `--cnpatroni-garnet-900` | `#4C1029` | 335, 65%, 18% | 14.86 | 1.24 |
| `--cnpatroni-garnet-950` | `#2C0917` | 336, 66%, 10% | 18.13 | 1.02 |

### Moss — brand accent

Moss spans hue 72 to 75 degrees. It is a deep yellow-green used for secondary
emphasis and the second series in diagrams. It is deliberately low-chroma so it
cannot be confused with StackGres's saturated greens or Percona's acid yellow.

| Token | Hex | HSL | Contrast vs white | Contrast vs basalt-950 |
|---|---|---|---|---|
| `--cnpatroni-moss-200` | `#DCE8B4` | 74, 53%, 81% | 1.29 | 14.24 |
| `--cnpatroni-moss-300` | `#B9C97A` | 72, 42%, 63% | 1.80 | 10.25 |
| `--cnpatroni-moss-400` | `#9AAE55` | 73, 35%, 51% | 2.45 | 7.50 |
| `--cnpatroni-moss-500` | `#7C8F3C` | 74, 41%, 40% | 3.59 | 5.13 |
| `--cnpatroni-moss-600` | `#62722C` | 74, 44%, 31% | 5.30 | 3.47 |
| `--cnpatroni-moss-700` | `#4C591F` | 73, 48%, 24% | 7.62 | 2.42 |
| `--cnpatroni-moss-800` | `#354013` | 75, 54%, 16% | 11.09 | 1.66 |

### Mark tones against every host background

Non-text contrast threshold is 3.00:1. These values justify shipping one mark
file rather than light and dark variants.

| Tone | white `#FFFFFF` | GitHub light `#F6F8FA` | basalt-50 `#FAF8F4` | GitHub dark `#0D1117` | GitHub dimmed `#22272E` | basalt-950 `#16140F` | black `#000000` |
|---|---|---|---|---|---|---|---|
| structure basalt-500 `#827868` | 4.34 | 4.08 | 4.09 | 4.36 | 3.46 | 4.24 | 4.84 |
| keystone garnet-500 `#CE4478` | 4.45 | 4.18 | 4.19 | 4.26 | 3.38 | 4.14 | 4.72 |

The lowest value across the whole matrix is 3.38:1, comfortably above 3.00:1.

### Semantic colours

There are two tones for each purpose, one for light surfaces and one for dark.
Informational status is neutral because of the no-blue rule.

| Purpose | Light tone | Contrast vs `#FAF8F4` | Dark tone | Contrast vs `#16140F` |
|---|---|---|---|---|
| success | `#1C7A50` | 5.01 | `#5DC48F` | 8.55 |
| warning | `#96591A` | 5.29 | `#DFA84E` | 8.63 |
| danger | `#B32B22` | 6.03 | `#F0908A` | 7.94 |
| info | `#6E6558` (basalt-600) | 5.40 | `#B0A492` (basalt-400) | 7.51 |

### Theme tokens

The light theme is defined on `:root`.

| Token | Value |
|---|---|
| `--cnpatroni-surface` | `#FAF8F4` |
| `--cnpatroni-surface-raised` | `#FFFFFF` |
| `--cnpatroni-surface-sunken` | `#F3EFE8` |
| `--cnpatroni-text` | `#26231F` |
| `--cnpatroni-text-secondary` | `#565043` |
| `--cnpatroni-text-muted` | `#6E6558` |
| `--cnpatroni-text-on-primary` | `#FFFFFF` |
| `--cnpatroni-link` | `#A9265A` |
| `--cnpatroni-link-hover` | `#8A1D49` |
| `--cnpatroni-brand-primary` | `#CE4478` |
| `--cnpatroni-brand-secondary` | `#827868` |
| `--cnpatroni-brand-accent` | `#62722C` |
| `--cnpatroni-border-subtle` | `#E6DFD3` |
| `--cnpatroni-border-strong` | `#827868` |
| `--cnpatroni-focus-ring` | `#A9265A` |
| `--cnpatroni-code-bg` | `#F3EFE8` |
| `--cnpatroni-code-text` | `#3D3832` |
| `--cnpatroni-status-success` | `#1C7A50` |
| `--cnpatroni-status-warning` | `#96591A` |
| `--cnpatroni-status-danger` | `#B32B22` |
| `--cnpatroni-status-info` | `#6E6558` |

The dark theme is defined in `@media (prefers-color-scheme: dark)` and again
under `:root[data-theme="dark"]` so an explicit toggle wins.
`--cnpatroni-brand-primary` and `--cnpatroni-brand-secondary` keep their
light-theme values in both themes, because they are the logo colours and the
logo does not change.

| Token | Value |
|---|---|
| `--cnpatroni-surface` | `#16140F` |
| `--cnpatroni-surface-raised` | `#26231F` |
| `--cnpatroni-surface-sunken` | `#26231F` |
| `--cnpatroni-text` | `#FAF8F4` |
| `--cnpatroni-text-secondary` | `#D2C8B7` |
| `--cnpatroni-text-muted` | `#B0A492` |
| `--cnpatroni-text-on-primary` | `#16140F` |
| `--cnpatroni-link` | `#EE97BA` |
| `--cnpatroni-link-hover` | `#F6C2D9` |
| `--cnpatroni-brand-accent` | `#B9C97A` |
| `--cnpatroni-border-subtle` | `#3D3832` |
| `--cnpatroni-border-strong` | `#6E6558` |
| `--cnpatroni-focus-ring` | `#EE97BA` |
| `--cnpatroni-code-bg` | `#26231F` |
| `--cnpatroni-code-text` | `#E6DFD3` |
| `--cnpatroni-status-success` | `#5DC48F` |
| `--cnpatroni-status-warning` | `#DFA84E` |
| `--cnpatroni-status-danger` | `#F0908A` |
| `--cnpatroni-status-info` | `#B0A492` |

In the dark theme the raised and sunken surfaces share a value; they are told
apart by `--cnpatroni-border-subtle`, not by fill.

### Verified text pairs

AA for normal text is 4.5:1; every pair below clears it.

Light theme:

| Foreground | Background | Ratio |
|---|---|---|
| text `#26231F` | surface `#FAF8F4` | 14.75 |
| text `#26231F` | raised `#FFFFFF` | 15.64 |
| text `#26231F` | sunken `#F3EFE8` | 13.65 |
| text-secondary `#565043` | surface `#FAF8F4` | 7.54 |
| text-muted `#6E6558` | surface `#FAF8F4` | 5.40 |
| link `#A9265A` | surface `#FAF8F4` | 6.36 |
| link `#A9265A` | raised `#FFFFFF` | 6.75 |
| link-hover `#8A1D49` | surface `#FAF8F4` | 8.42 |
| accent `#62722C` | surface `#FAF8F4` | 5.00 |
| code-text `#3D3832` | code-bg `#F3EFE8` | 10.12 |
| text-on-primary `#FFFFFF` | link `#A9265A` | 6.75 |

Dark theme:

| Foreground | Background | Ratio |
|---|---|---|
| text `#FAF8F4` | surface `#16140F` | 17.35 |
| text `#FAF8F4` | raised `#26231F` | 14.75 |
| text-secondary `#D2C8B7` | surface `#16140F` | 11.12 |
| text-muted `#B0A492` | surface `#16140F` | 7.51 |
| link `#EE97BA` | surface `#16140F` | 8.56 |
| link `#EE97BA` | raised `#26231F` | 7.28 |
| link-hover `#F6C2D9` | surface `#16140F` | 11.95 |
| accent `#B9C97A` | surface `#16140F` | 10.25 |
| code-text `#E6DFD3` | code-bg `#26231F` | 11.81 |
| text-on-primary `#16140F` | `#E06894` | 5.76 |

Non-text user-interface contrast has a threshold of 3.00:1.

| Pair | Ratio |
|---|---|
| border-strong `#827868` on surface `#FAF8F4` | 4.09 |
| border-strong `#6E6558` on surface `#16140F` | 3.21 |
| focus ring `#A9265A` on surface `#FAF8F4` | 6.36 |
| focus ring `#EE97BA` on surface `#16140F` | 8.56 |

`--cnpatroni-border-subtle` is decorative only, at 1.25:1 against
`--cnpatroni-surface` in light and 1.59:1 against `--cnpatroni-surface` in dark.
It must never be the sole indicator of a control's boundary;
`--cnpatroni-border-strong` exists for that purpose.

## Typography

### Families

- Sans: **IBM Plex Sans**, licensed under the SIL Open Font License 1.1. The
  lockup uses the pinned `@ibm/plex-sans@1.1.0` release, published 2024-11-13,
  font version 3.005.
- Mono: **IBM Plex Mono**, licensed under the SIL Open Font License 1.1, pinned
  release `@ibm/plex-mono@1.1.0`.

Both are freely licensable, self-hostable and redistributable under the OFL, and
neither appears in any of the identities in the avoid-list below.

The fallback stacks are:

```css
--cnpatroni-font-sans: "IBM Plex Sans", "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", sans-serif;
--cnpatroni-font-mono: "IBM Plex Mono", ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace;
```

`tokens.css` contains no `@font-face` rule and no `@import`. The repository does
not vendor the fonts. Whoever hosts the documentation site must self-host them
and ship the SIL Open Font License 1.1 text alongside.

### Scale

Base 1 rem equals 16 px. The ratio is approximately 1.2, snapped to a 0.125 rem
grid so every value lands on a whole pixel at the default root size.

| Token | Size | Line height | Pixels | Use |
|---|---|---|---|---|
| `--cnpatroni-size-100` | `0.75rem` | `1.25rem` | 12 / 20 | captions, table footnotes |
| `--cnpatroni-size-200` | `0.875rem` | `1.375rem` | 14 / 22 | small text, code inside body |
| `--cnpatroni-size-300` | `1rem` | `1.625rem` | 16 / 26 | body |
| `--cnpatroni-size-400` | `1.125rem` | `1.75rem` | 18 / 28 | lead paragraph |
| `--cnpatroni-size-500` | `1.25rem` | `1.75rem` | 20 / 28 | heading level 4 |
| `--cnpatroni-size-600` | `1.5rem` | `2rem` | 24 / 32 | heading level 3 |
| `--cnpatroni-size-700` | `1.875rem` | `2.375rem` | 30 / 38 | heading level 2 |
| `--cnpatroni-size-800` | `2.375rem` | `2.875rem` | 38 / 46 | heading level 1 |
| `--cnpatroni-size-900` | `3rem` | `3.375rem` | 48 / 54 | display |

Each size is emitted as `--cnpatroni-size-N` and each line height as
`--cnpatroni-leading-N` with the same N.

### Weights and tracking

| Token | Value |
|---|---|
| `--cnpatroni-weight-regular` | `400` |
| `--cnpatroni-weight-medium` | `500` |
| `--cnpatroni-weight-semibold` | `600` |
| `--cnpatroni-weight-bold` | `700` |
| `--cnpatroni-tracking-tight` | `-0.01em` |
| `--cnpatroni-tracking-normal` | `0` |
| `--cnpatroni-tracking-wide` | `0.01em` |

### Type rules

- Headings use sentence-style capitalization, in line with the project's
  engineering rules.
- Headings use sans, SemiBold 600, tracking tight at sizes 700, 800 and 900, and
  tracking normal below.
- Body uses sans, Regular 400, size 300 and tracking normal. Its measure is 66
  to 80 characters.
- Code uses mono, Regular 400, size 200 inside body text, size 300 in standalone
  blocks and tracking normal.
- Captions use sans, Regular 400, size 100, tracking wide and colour
  `--cnpatroni-text-muted`.
- Never set body text in the mono family, and never set headings in all
  capitals.

### Structural tokens

| Token | Value |
|---|---|
| `--cnpatroni-radius-sm` | `2px` |
| `--cnpatroni-radius-md` | `4px` |
| `--cnpatroni-radius-lg` | `8px` |
| `--cnpatroni-border-width` | `1px` |
| `--cnpatroni-focus-width` | `2px` |
| `--cnpatroni-focus-offset` | `2px` |

Radii stay small on purpose. This brand does not use the large "squircle" corner
radius, because a rounded-square tile is the container form of the Patroni
project's own mark and the two must not be confusable.

## Documentation design

[`tokens.css`](tokens.css) is the brand's portable documentation-design layer. A
consumer can include it with `<link rel="stylesheet" href="tokens.css">` or
through its build system, then use the declared `--cnpatroni-*` custom
properties.

The file declares custom properties only. It contains no component styles, no
`@font-face`, no external reference and no `@import`. The default light theme
lives on `:root`; the system dark theme lives under
`@media (prefers-color-scheme: dark)`, and the identical dark values appear
again under `:root[data-theme="dark"]` so an explicit dark selection wins.

## Outstanding: the website footer

Owner directive D-1 requires the independence statement to appear in the README
and in the website footer. This repository satisfies the README half only.
`CLAUDE.md` rule 5 records that the documentation website is not built from this
repository — there is no site configuration in this tree — so the footer
requirement cannot be met here. It remains outstanding for whoever owns the site
repository, and it is not closed by the README carrying the same text.

## What was avoided, and how it was checked

All observations below were made on 2026-08-09 by fetching the named source and
reading the file or the rendered image.

| Identity | What was checked | What it uses | What this brand does instead |
|---|---|---|---|
| CloudNativePG | `cloudnativepg-logo.svg` and `portrait/cloudnativepg-portrait-blue.png` in `github.com/cloudnative-pg/artwork`, read directly | Elephant mascot "Peggie", derived from the Postgres Slonik; vertical radial gradients from `#732DD9` through `#6A2BCB` and `#5125A5` to `#291C69`; wordmark in a bold neo-grotesque in navy `#121646` | No mascot, no animal, no gradient of any kind, no violet, no navy; flat geometric masonry; two-weight IBM Plex Sans wordmark in warm grey |
| Patroni | `docs/_static/patroni-logo.svg` and `patroni-logo.png` in `github.com/patroni/patroni`, read directly | A rounded-square tile enclosing two interlocking organic forms, in two tones of Postgres blue `#336791` and `#5E92BB` | No enclosing tile, no large corner radius, no interlocking forms, no blue at all |
| Crunchy Data PGO | `crunchydata.com/branding` | Hippo icon in solid and stroke variants; Electric Blue `#3A6AE8`, Dark Blue `#002259`, Charcoal `#58595B`, Seafoam `#93F7D1` | No animal, no blue, no seafoam; warm neutral ramp instead of charcoal |
| Zalando postgres-operator | `docs/diagrams/logo.png` in `github.com/zalando/postgres-operator`, rendered and its colours sampled | A grey robotic arm holding a rounded tile containing a white elephant silhouette; greyscale only, dominant tones `#909090` and `#808080` | No illustration, no elephant, no tile, no cool grey; the neutral ramp is warm |
| StackGres | inline colours on `stackgres.io` | Blue and teal `#16657C`, `#428BB4`, `#42A8C8` with orange `#FF7124`, greens `#39B54A` and `#009245`, dark navy `#141E35` | No blue, no teal, no orange; the accent is a low-chroma yellow-green far from those greens |
| Percona | inline colours on `percona.com`; legacy primary from a secondary brand-colour source | Current identity is violet-led, `#653DF4` and `#6C5CE7`, with acid yellow `#F6FE54` on dark navy `#00162B`; the legacy primary was Guardsman Red `#C10000` | No violet, no acid yellow, no navy; garnet at hue 337 is well clear of both the legacy red at hue 0 and the violets |
| Timescale, now TigerData | inline colours on `tigerdata.com` | Near-black `#0A0A0C` with cool zinc neutrals `#71717A` and `#F4F4F5`, orange `#FA500F` and `#E2401B`, yellow `#FFD800` | The dark surface is a warm `#16140F` at hue 43, not a cool near-black, and the neutral ramp is warm at hue 33 to 43 rather than cool zinc; no orange, no yellow |

Every identity in that list is either a creature or a gradient wordmark. This
mark is flat, geometric and has no mascot, which differentiates it before any
individual colour or shape comparison is made.

The elephant, the whale and the Kubernetes helm were excluded from consideration
at the outset as the cliché of this category.

## Open items

- Whether the project asserts a trademark in the name or the mark, and any
  logo-usage policy for third parties, is not decided. The assets are currently
  covered by `docs/LICENSE` alone.
- A favicon and social preview image are not produced yet; they should be
  derived from `mark.svg` rather than redrawn.
