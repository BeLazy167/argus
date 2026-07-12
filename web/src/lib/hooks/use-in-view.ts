"use client";

import { useEffect, useRef, useState } from "react";

/**
 * Lightweight intersection hook. Sets `inView` once the returned ref's element
 * enters the viewport so descendant animations can pause/resume via CSS
 * selectors (pair with a `data-in-view` attribute and
 * `[data-in-view="false"] … { animation-play-state: paused }` rules).
 */
export function useInView<T extends HTMLElement>(rootMargin = "0px") {
	const ref = useRef<T>(null);
	const [inView, setInView] = useState(false);

	useEffect(() => {
		const el = ref.current;
		if (!el) return;
		const obs = new IntersectionObserver(
			([entry]) => {
				setInView(!!entry?.isIntersecting);
			},
			{ threshold: 0.15, rootMargin },
		);
		obs.observe(el);
		return () => obs.disconnect();
	}, [rootMargin]);

	return { ref, inView };
}
