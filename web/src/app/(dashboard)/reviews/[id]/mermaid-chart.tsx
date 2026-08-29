"use client";

import { memo, useEffect, useRef, useState } from "react";
import DOMPurify from "dompurify";

/**
 * Renders one LLM-emitted Mermaid diagram, or a readable fallback.
 *
 * Lives in its own module so the render/cleanup contract can be unit tested
 * without mounting the whole review page.
 *
 * Invariant this component owes the user: the container is never left blank.
 * Every path either paints an SVG or paints the fallback with the raw source.
 */
export const MermaidChart = memo(function MermaidChart({ chart }: { chart: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    let cancelled = false;
    // Stable id per render so the cleanup pass can locate any orphan node
    // Mermaid leaves on document.body on a failure path (belt-and-suspenders
    // on top of suppressErrorRendering).
    const renderId = "mermaid-" + Math.random().toString(36).slice(2);
    setError(false);

    import("mermaid")
      .then(async (m) => {
        if (cancelled) return undefined;
        m.default.initialize({
          startOnLoad: false,
          // Mermaid 11+ otherwise injects a bomb-icon "Syntax error in text"
          // SVG into document.body on parse failure — outside our ref — and
          // it sticks around even after our promise rejects. Kills it.
          suppressErrorRendering: true,
          theme: "dark",
          themeVariables: {
            primaryColor: "#44403c",
            primaryTextColor: "#f5f0eb",
            primaryBorderColor: "#57534e",
            lineColor: "#78716c",
            secondaryColor: "#292524",
            tertiaryColor: "#1c1917",
            nodeTextColor: "#f5f0eb",
            nodeBorder: "#57534e",
            mainBkg: "#44403c",
            clusterBkg: "#292524",
            clusterBorder: "#57534e",
            titleColor: "#f5f0eb",
            edgeLabelBackground: "#292524",
            textColor: "#f5f0eb",
          },
        });
        // Preflight: parse() returns false on invalid syntax with no DOM
        // side effects. Catches LLM-emitted diagrams that pass the
        // backend's coarse bracket-balance check but still fail Mermaid's
        // grammar (reserved words, edge-label chars, unsupported nodes).
        const parsed = await m.default.parse(chart, { suppressErrors: true });
        if (cancelled || !ref.current) return undefined;
        if (!parsed) {
          setError(true);
          return undefined;
        }
        ref.current.textContent = "";
        return m.default.render(renderId, chart);
      })
      .then((result) => {
        if (cancelled || !ref.current || !result) return;
        const clean = DOMPurify.sanitize(result.svg, {
          USE_PROFILES: { svg: true, svgFilters: true },
          ADD_TAGS: ["foreignObject"],
        });
        ref.current.innerHTML = clean;
        // A container with nothing in it is the one outcome the user cannot
        // act on: a blank bordered box that reads as a broken product. If
        // sanitizing (or anything downstream) leaves us with no element, fall
        // back to showing the source instead of painting nothing.
        if (ref.current.childElementCount === 0) setError(true);
      })
      .catch(() => {
        if (!cancelled) setError(true);
      })
      .finally(() => {
        // Mermaid's temp render container lives at #${renderId}; some
        // versions also create a `d${renderId}` measurement node.
        // Remove either if it outlived the render. Cheap, runs once.
        //
        // Scope matters: mermaid.render() returns an SVG string whose ROOT
        // CARRIES id=${renderId}, so once we insert it a bare
        // getElementById(renderId) resolves to our own diagram. Deleting that
        // is what made every diagram render as an empty box. Only nodes
        // outside our container are orphans.
        removeIfOutside(ref.current, renderId);
        removeIfOutside(ref.current, "d" + renderId);
      });
    return () => { cancelled = true; };
  }, [chart]);

  if (error) {
    return (
      <div className="space-y-2">
        <p className="text-[11px] font-mono text-slate-text">Diagram could not be rendered</p>
        <pre className="text-[11px] font-mono text-slate-text/80 whitespace-pre-wrap break-words">
          {chart}
        </pre>
      </div>
    );
  }
  return <div ref={ref} className="flex justify-center" />;
});

/**
 * Removes a stray node that Mermaid left behind in the document.
 *
 * Skips anything inside `container`, because Mermaid's returned SVG reuses the
 * render id we passed it — deleting by id alone erases the diagram we just
 * painted.
 *
 * @param container The element holding the rendered diagram, if still mounted.
 * @param id Element id to look up.
 */
function removeIfOutside(container: HTMLElement | null, id: string) {
  const node = document.getElementById(id);
  if (!node) return;
  if (container?.contains(node)) return;
  node.remove();
}
