# Fix INP Issue: 584ms Blocking on Navbar Hover

## Objective

Reduce the INP (Interaction to Next Paint) from 584ms to under 200ms (Google's "good" threshold) for the navbar CTA button hover interaction. The reported element is the gradient overlay div in `navbar.tsx` at lines 160 and 169.

## Root Cause Analysis

The 584ms is **not** caused by the gradient div itself (it has no JS handlers). It's caused by compounding main-thread contention from multiple sources firing simultaneously during mouse movement toward and over the navbar:

| Factor | Impact | File:Line |
|--------|--------|-----------|
| **EyeSymbol global `mousemove`** -- fires on every pixel, calls `getBoundingClientRect()` (forced layout) + `setState` (React re-render) | **High** | `eye-symbol.tsx:14-28`, `landing-content.tsx:253` |
| **Three.js O(n^2) rAF loop** -- 54 nodes, ~1,431 distance calcs per frame, runs perpetually even when hero is off-screen | **High** | `constellation.tsx:179-253` |
| **`backdrop-blur-xl` on navbar** -- forces full backdrop repaint when anything changes behind/inside it | **Medium** | `navbar.tsx:68` |
| **`oklch()` box-shadow transition** on hover -- box-shadow is not GPU-compositable, triggers paint per frame | **Medium** | `navbar.tsx:157` |
| **AnimatedReview rAF** -- if visible, calls `forceRender()` (React re-render) every frame | **Low-Medium** | `animated-review.tsx:92-126` |

## Implementation Plan

### 1. Throttle `EyeSymbol` mousemove handler (highest impact, simplest fix)

- [ ] **1a.** In `eye-symbol.tsx`, add a `requestAnimationFrame`-based throttle to `handleMouseMove`. Currently every pixel of mouse movement triggers `getBoundingClientRect()` + `setOffset()` (React re-render). The fix: store the latest mouse event in a ref, only process it inside a single rAF callback. This collapses N mousemove events per frame into exactly 1 state update per frame.

  ```
  Approach: Use a ref to track the rAF ID. On mousemove, store event coords in a ref.
  If no rAF is pending, request one. In the rAF callback, read coords from the ref,
  compute offset, call setOffset, clear the rAF ref.
  ```

  **Rationale:** This is the single biggest win. The current handler fires 60-100+ times/second during mouse movement, each triggering a React re-render. With rAF throttling, it fires at most once per frame (16.6ms).

### 2. Pause Three.js when hero is off-screen (high impact)

- [ ] **2a.** In `constellation.tsx`, add a `frameloop="demand"` prop to the `<Canvas>` component. This tells `@react-three/fiber` to only render when explicitly invalidated, instead of running a perpetual rAF loop.

- [ ] **2b.** Add an `IntersectionObserver` in `ConstellationBackground` that tracks whether the hero section is visible. When visible, call `invalidate()` each frame (via `useFrame` + `state.invalidate()`). When not visible, stop calling `invalidate()` so the canvas goes idle.

  **Rationale:** The O(n^2) node distance computation runs 60fps even when the user has scrolled past the hero. This consumes ~2-4ms of main-thread time every 16ms, directly eating into the interaction budget. Pausing when off-screen eliminates this contention for all below-fold interactions.

  **Alternative approach:** Instead of `frameloop="demand"`, use a simpler approach -- wrap the canvas in a container with an IntersectionObserver and conditionally set a `paused` prop or unmount the Canvas entirely when off-screen. Less elegant but simpler to implement.

### 3. Replace `backdrop-blur-xl` with a solid background (medium impact)

- [ ] **3a.** In `navbar.tsx:68`, replace `backdrop-blur-xl` with a higher-opacity solid background. Change `bg-void/90 backdrop-blur-xl` to `bg-void/95` (or `bg-void/[0.97]`).

  **Rationale:** `backdrop-blur` forces the browser to blur every pixel behind the navbar on every composite. When elements inside the navbar change (hover state), it triggers a full repaint of the blurred region. A solid semi-transparent background achieves a very similar visual effect at near-zero paint cost.

  **Alternative:** If the blur effect is important to the design, use `backdrop-blur-sm` (4px) instead of `backdrop-blur-xl` (24px). The blur radius directly scales the paint cost.

### 4. Move box-shadow hover to GPU-compositable properties (medium impact)

- [ ] **4a.** In `navbar.tsx:157` and `navbar.tsx:166`, replace the `hover:shadow-[0_0_20px_-4px_oklch(0.77_0.15_75/0.5)]` transition with a pseudo-element approach. Create the shadow as a static `::after` pseudo-element with `opacity: 0`, and transition only `opacity` on hover. Opacity transitions are GPU-compositable (no paint).

  **Alternatively (simpler):** Add `will-change: transform` to the CTA button elements so the browser promotes them to their own compositing layer, isolating the box-shadow paint from the navbar's backdrop-blur layer.

- [ ] **4b.** In `navbar.tsx:160` and `navbar.tsx:169`, the gradient overlay divs already use opacity transitions (which are GPU-compositable). No change needed for these elements specifically. The issue is that they're inside the backdrop-blur container, so their opacity change triggers a backdrop repaint. Fix 3a above resolves this.

### 5. Optimize AnimatedReview rAF loop (low-medium impact)

- [ ] **5a.** In `animated-review.tsx:115`, the `forceRender()` call runs inside a `requestAnimationFrame` loop. It already has a `changed` guard, but when typing is in progress, it calls `forceRender` on every single frame (60fps re-renders). Add a simple optimization: once all comments are fully typed and visible, ensure the rAF loop terminates (it already does at line 123, verify this path works correctly).

- [ ] **5b.** Consider replacing `forceRender((n) => n + 1)` with direct DOM manipulation for the typing cursor position, avoiding React re-renders entirely during the typing animation. Use refs to update the text content directly.

  **Rationale:** This only matters if the animated review section is visible while the user hovers the navbar (e.g., user scrolls partially). Lower priority than fixes 1-4.

### 6. Add `content-visibility: auto` to off-screen sections (low impact, easy win)

- [ ] **6a.** In `landing-content.tsx`, add `style={{ contentVisibility: 'auto', containIntrinsicSize: 'auto 500px' }}` to sections below the fold (Social Proof, See It In Action, Why Argus, Pipeline, Pricing, CTA). This tells the browser to skip layout and paint for off-screen sections.

  **Rationale:** Free performance win. The browser skips rendering work for sections the user hasn't scrolled to yet, reducing main-thread contention during initial page interactions.

## Verification Criteria

- INP on the "Get started" / "Dashboard" navbar button hover drops below 200ms (measure with Chrome DevTools Performance panel or web-vitals library)
- `npx next build` passes clean
- Visual appearance of navbar hover effect is unchanged or imperceptibly different
- Three.js constellation still animates when hero section is visible
- Three.js constellation stops consuming CPU when scrolled past
- EyeSymbol still tracks mouse smoothly (no visible jank from throttling)
- `prefers-reduced-motion` still respected (verify in globals.css)

## Potential Risks and Mitigations

1. **Visual regression from removing backdrop-blur**
   Mitigation: Use `bg-void/[0.97]` which is nearly opaque. On dark backgrounds, the difference is imperceptible. A/B test if needed.

2. **Three.js `frameloop="demand"` may not work with all `useFrame` hooks**
   Mitigation: Ensure every `useFrame` callback calls `state.invalidate()` when it wants another frame. Test that the animation still runs smoothly when the hero is visible.

3. **EyeSymbol throttling may feel less responsive**
   Mitigation: rAF throttling still gives 60fps updates. The human eye cannot perceive sub-16ms latency differences in cursor tracking. This is a safe throttle.

4. **`content-visibility: auto` can cause layout shifts on scroll**
   Mitigation: Set `containIntrinsicSize` to approximate section heights. This gives the browser a placeholder size before the section is rendered.

## Priority Order

Implement in this order for maximum impact with minimum risk:

1. Fix 1 (throttle mousemove) -- single biggest win, isolated change
2. Fix 2 (pause Three.js off-screen) -- removes perpetual CPU drain
3. Fix 3 (remove backdrop-blur) -- eliminates expensive paint layer
4. Fix 4 (GPU-compositable shadows) -- reduces per-frame paint cost
5. Fix 6 (content-visibility) -- free win, no behavior change
6. Fix 5 (AnimatedReview) -- only matters in specific scroll positions

Fixes 1+2+3 alone should bring INP well under 200ms.
