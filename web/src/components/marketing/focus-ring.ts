/**
 * Shared keyboard focus-ring classes for marketing links and controls.
 *
 * `focusRing` matches the navbar idiom — a hairline amber ring on dark
 * surfaces. `focusRingAmber` offsets the ring so it stays visible on
 * amber-filled buttons (an inset amber ring would blend into the fill).
 * Both drop the mouse-focus outline and only paint on keyboard focus.
 */
export const focusRing = "focus:outline-none focus-visible:ring-1 focus-visible:ring-amber/50";

export const focusRingAmber =
	"focus:outline-none focus-visible:ring-2 focus-visible:ring-amber focus-visible:ring-offset-2 focus-visible:ring-offset-background";
