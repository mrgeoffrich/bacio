# Motion layout animations — vendor reference

Vendor reference for the **card-movement animations** on the React surfaces (kanban
board, the Pipeline page, the ship flourish). The library is **Motion** (the package
formerly published as `framer-motion`), pinned at **v11.18.2** in
`desktop/frontend/package.json`.

> **Version pin.** motion.dev now serves **v12** docs. The layout-animation API
> below is stable across v11→v12, but if you copy a snippet from the live site,
> sanity-check it against what's actually installed. Don't bump `motion` to chase a
> doc example — verify against `desktop/frontend/node_modules/motion/package.json`.

Official pages (read these for the authoritative source):

- Layout animations — <https://motion.dev/docs/react-layout-animations>
- Transitions — <https://motion.dev/docs/react-transitions>
- AnimatePresence — <https://motion.dev/docs/react-animate-presence>

---

## 0. How Motion is wired in this repo (read first)

`src/App.jsx` mounts the whole tree inside:

```jsx
import { LazyMotion, domMax, LayoutGroup } from 'motion/react';

<LazyMotion features={domMax} strict>
  {/* app */}
</LazyMotion>
```

Two consequences that bite if you forget them:

1. **`strict` mode forbids the full `motion.*` component.** You must use the short
   form **`m.article`, `m.div`, …** imported as `import { m } from 'motion/react'`.
   Writing `motion.div` inside the `strict` provider throws at runtime. This is why
   `KanbanCard` and `PipelineView` use `<m.article>` everywhere.
2. **`domMax` is the feature bundle that includes layout animations + drag.** The
   smaller `domAnimation` bundle does *not* include layout projection, so `layout` /
   `layoutId` would silently no-op. We use `domMax`, so layout animations are live.

`LayoutGroup` is also imported here — see §5.

---

## 1. The `layout` prop — automatic FLIP movement

Add `layout` to a motion element and Motion animates **size and position** whenever
its layout changes between renders (list reorder, item add/remove, flex/grid change,
width/height change):

```jsx
<m.div layout />
```

- Layout animations run on the GPU via the CSS `transform` property — they do **not**
  animate `width`/`top`/`left` directly, so they stay smooth.
- A "layout change" includes: changing `width`/`height`, number of grid columns,
  reordering a list, or adding/removing items. You don't animate the position
  yourself — you change the DOM/layout and Motion measures the before/after (FLIP).

Variants:

- **`layout` (default `true`)** — animate both position and size.
- **`layout="position"`** — animate position only; size snaps. Use this for elements
  whose **aspect ratio changes** (images, text reflowing) where animating size
  produces a squash/stretch distortion.
- **`layout="size"`** — animate size only.

In this repo `KanbanCard` uses bare `layout` (position + size) — see
`src/components/KanbanCard.jsx`.

---

## 2. `layoutId` — shared-element transitions across subtrees

Give two elements in **different parts of the tree** the same `layoutId` and Motion
animates the transition from one to the other when one unmounts and the other mounts:

```jsx
{isSelected && <m.div layoutId="underline" />}
```

> In a shared (`layoutId`) transition, **the transition of the element being animated
> *to* is the one that's used** — not the source's. Tune timing on the destination.

This is exactly the kanban → Shipped-pill "ship flourish": `KanbanCard` sets
`layoutId={card.key}`; when the card transitions to `done`, the destination slot
inside `ShippedPopover`/`ShippedPill` renders with the **same** `layoutId`, and Motion
flies the card between the two subtrees. See the comment block at
`src/components/KanbanCard.jsx:217`.

> **`LazyMotion` + `layoutId` namespacing gotcha.** There's a note at
> `src/App.jsx:972` — Motion namespaces `layoutId`s per subtree/group; a stray
> `LayoutGroup` boundary or duplicate id can stop the match. If a shared transition
> "teleports" instead of flying, check both ends share the *exact* id and aren't
> split across two `LayoutGroup`s.

For `layoutId` elements that should animate **out**, wrap in `AnimatePresence` (§4):

```jsx
<AnimatePresence>
  {isOpen && <m.div layoutId="modal" />}
</AnimatePresence>
```

---

## 3. Configuring the movement animation (`transition.layout`)

This is the knob for "how the card moves." A `transition` applies to *all* animating
values; give layout its **own** sub-key so the move timing is independent of
opacity/scale:

```jsx
<m.article
  layout
  animate={{ opacity: 1, scale: 1 }}
  transition={{
    layout: { duration: 0.28, ease: 'easeInOut' }, // the MOVE
    opacity: { duration: 0.18 },                    // the fade
    scale:   { duration: 0.18 },
  }}
/>
```

That is verbatim what `KanbanCard` does. `PipelineView` passes `layoutEase` down so
the easing curve is consistent across surfaces.

### 3a. Tween (duration + easing curve) — what we use now

| Option     | Default | Notes |
|------------|---------|-------|
| `duration` | `0.3`   | Seconds. |
| `ease`     | `easeOut` (for tween) | Name, cubic-bezier array, or fn. |
| `times`    | —       | Keyframe positions 0–1, for multi-step keyframes. |

Named easings: `"linear"`, `"easeIn"`, `"easeOut"`, `"easeInOut"`, `"circIn/Out/InOut"`,
`"backIn/Out/InOut"`, `"anticipate"`. Custom cubic-bezier: `[0.17, 0.67, 0.83, 0.67]`.

### 3b. Spring (physics) — usually smoother for card movement

Springs are interruption-friendly and tend to feel better than a fixed-duration tween
when a user can drag/drop rapidly (a half-finished move that gets redirected blends
naturally instead of restarting). Switch the layout transition to a spring:

```jsx
transition={{
  layout: { type: 'spring', visualDuration: 0.3, bounce: 0.2 },
}}
```

| Option           | Default | Notes |
|------------------|---------|-------|
| `type`           | dynamic | Set `"spring"` explicitly, or `"tween"`. |
| `bounce`         | `0.25`  | 0 = no overshoot, 1 = very bouncy. Pair with `visualDuration`. |
| `visualDuration` | —       | "How long the bulk of the move takes," in seconds. **Easier to reason about than stiffness/damping** — set this + `bounce` and ignore the raw physics. |
| `stiffness`      | `1`     | Higher = snappier/more sudden. |
| `damping`        | `10`    | Higher = less oscillation; large values ≈ critically damped (no bounce). |
| `mass`           | `1`     | Higher = heavier/slower. |
| `velocity`       | current | Initial velocity. |
| `restSpeed`      | `0.1`   | Speed threshold to call it "done." |
| `restDelta`      | `0.01`  | Distance threshold to call it "done." |

**Recommendation for smooth card movement:** prefer the
`{ type: 'spring', visualDuration, bounce }` form over hand-tuning
`stiffness`/`damping` — it's far easier to dial in and reads consistently across
cards of different travel distances.

### 3c. App-wide defaults via `MotionConfig`

```jsx
import { MotionConfig } from 'motion/react';
<MotionConfig transition={{ duration: 0.4, ease: 'easeInOut' }}>…</MotionConfig>
```

Useful if we want one source of truth for the whole kanban transition family instead
of repeating timings per component.

---

## 4. `AnimatePresence` — exit animations + reflow

Cards leaving a column (or moving between Pipeline sections) need to animate **out**.
A component removed from the React tree normally vanishes instantly; `AnimatePresence`
keeps it mounted long enough to play its `exit` prop.

```jsx
<AnimatePresence mode="popLayout" initial={false}>
  {list.map(card => <KanbanCard key={card.key} card={card} … />)}
</AnimatePresence>
```

Rules that matter (all of these are load-bearing in `PipelineView`/`Board`):

- **Stable, unique `key` per child.** Use `card.key`, never the array index —
  reordering with index keys breaks the key↔component association and the animation
  misfires.
- **`AnimatePresence` must stay mounted** while it controls exits. Put the
  conditional *inside* it (`<AnimatePresence>{cond && …}</AnimatePresence>`), not
  around it.
- **`initial={false}`** suppresses the enter animation for items already present on
  first render (so the board doesn't animate every card on mount).

### `mode`

| `mode`        | Behaviour |
|---------------|-----------|
| `"sync"` (default) | Enter/exit fire immediately; no sequencing. |
| `"wait"`      | New element waits until the exiting one finishes. |
| `"popLayout"` | **Exiting element is pulled out of layout flow immediately**, so siblings reflow at once. Pairs with `layout` — this is what makes the gap close smoothly as a card leaves. |

### `popLayout` requires a forwarded ref

When `mode="popLayout"`, a **custom component child must forward a ref to its DOM
node** (`React.forwardRef`), or Motion can't pop it out of flow. This is why
`KanbanCard` takes a `ref` and forwards it to `<m.article ref={ref}>`. If you wrap
cards in a new custom component, forward the ref or popLayout breaks.

Also, with `popLayout`, the **animating parent must have `position` other than
`static`** (e.g. `relative`) so popped children keep their position.

---

## 5. `LayoutGroup` — sync layout across sibling components

If two independent components need to animate layout in response to *each other's*
changes (e.g. one accordion expanding pushes a sibling), wrap them:

```jsx
import { LayoutGroup } from 'motion/react';
<LayoutGroup>
  <Accordion /><Accordion />
</LayoutGroup>
```

`App.jsx` already imports `LayoutGroup`. Note it also **namespaces `layoutId`s** — see
the §2 gotcha.

Related advanced props (rarely needed here):

- `layoutScroll` — put on a scrollable container so Motion accounts for scroll offset.
- `layoutRoot` — put on a `position: fixed` container for the same reason.
- `layoutDependency` — pass a value so Motion only re-measures when *it* changes
  (perf: avoids measuring on every render).
- `layoutAnchor={{ x, y }}` — control the origin point (0–1) a shared element animates
  from/around.

---

## 6. Gotchas that produce janky / broken movement

These are the usual suspects when "the cards don't move smoothly":

1. **`display: inline` can't be layout-animated.** The element needs to be a block /
   flex / grid item. Inline elements silently don't animate.
2. **Children get scale-distorted during a size animation.** When a parent animates
   size, its children are squashed/stretched by the transform. Fix: add `layout` to
   the child too, so Motion counter-scales it.
3. **`borderRadius` and `boxShadow` distort mid-flight** unless they're set via
   `style` / `animate` (not a CSS class or CSS variable). Motion can only scale-correct
   them when it owns the value. `KanbanCard` works around this by applying an **inline**
   `borderRadius`/`boxShadow` *only while animating* (gated on
   `onLayoutAnimationStart`/`onLayoutAnimationComplete`) — see
   `src/components/KanbanCard.jsx`. If a moving card's corners or shadow flicker, this
   is why.
4. **Aspect-ratio changes squash content** → use `layout="position"` so size snaps and
   only position animates.
5. **Shared (`layoutId`) transition uses the *destination's* transition**, not the
   source's. Tuning the source does nothing.
6. **SVG elements aren't supported** for layout animations.
7. **Horizontal window resize blocks layout animations** (intentional perf
   optimisation) — don't debug a "missing" animation that only reproduces while
   dragging the window edge.
8. **Scrollbar appear/disappear can trigger spurious layout animations.** Reserve the
   gutter in CSS:
   ```css
   body { overflow-y: auto; scrollbar-gutter: stable; }
   ```
9. **`AnimatePresence` exit not firing** → almost always a missing/duplicate `key`, or
   the `AnimatePresence` itself being conditionally unmounted (see §4).

---

## 7. Where this is used in the codebase

| File | What it does with Motion |
|------|--------------------------|
| `src/App.jsx` | `LazyMotion features={domMax} strict` provider; `LayoutGroup`; ship-flourish `layoutId` namespacing note. |
| `src/components/KanbanCard.jsx` | The card itself: `m.article` with `layout`, `layoutId={card.key}`, `transition.layout`, enter/exit, and the borderRadius/boxShadow mid-flight workaround. Forwards `ref` for `popLayout`. |
| `src/components/PipelineView.jsx` | Pipeline page: three drop zones, `AnimatePresence mode="popLayout"` per section, `m.article` stage wrapper, passes `layoutEase` to cards. |
| `src/components/Board.jsx` | Kanban columns: per-column `AnimatePresence`; cards reorder/move via shared `layoutId`. |
| `src/components/ShippedPopover.jsx`, `ShippedPill.jsx` | Destination ends of the ship-flourish `layoutId` flight. |
| `src/lib/shipFlourish.ts` | Helpers for the ship animation. |
