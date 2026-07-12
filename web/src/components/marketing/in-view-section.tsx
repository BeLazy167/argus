"use client";

import type { ComponentProps } from "react";
import { useInView } from "@/lib/hooks/use-in-view";

/**
 * `<section>` wrapper that flips a `data-in-view` attribute as it enters and
 * leaves the viewport. Descendant decorative loop animations pause while the
 * section is off-screen via the `[data-in-view="false"] …` rules in
 * globals.css (and each section's local `<style>`), keeping the main thread
 * idle when the animation can't be seen.
 *
 * Server-rendered section content can be passed as `children` — only this
 * thin wrapper is a client boundary.
 */
export function InViewSection({ children, ...props }: ComponentProps<"section">) {
	const { ref, inView } = useInView<HTMLElement>();
	return (
		<section ref={ref} data-in-view={inView ? "true" : "false"} {...props}>
			{children}
		</section>
	);
}
