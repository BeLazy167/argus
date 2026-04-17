"use client";

/**
 * Animated shader lines — concentric pulsing rings in RGB. Port of a three.js donor
 * fragment shader to OGL (already bundled for <GradientMesh />; avoids pulling ~600KB
 * of three.js for a single-file port).
 *
 * The shader itself is unchanged from the donor — pixel-for-pixel identical output —
 * only the WebGL plumbing is rewritten as typed ES imports. Honors prefers-reduced-motion
 * by rendering a single static frame and skipping the rAF loop.
 */
import { Renderer, Program, Mesh, Triangle, Vec2 } from "ogl";
import { useEffect, useRef } from "react";

const VERT_SHADER = `
attribute vec2 position;
void main() {
  gl_Position = vec4(position, 0.0, 1.0);
}
`;

// Donor shader, verbatim. Keeps the visual identical.
const FRAG_SHADER = `
#define TWO_PI 6.2831853072
#define PI 3.14159265359

precision highp float;
uniform vec2 resolution;
uniform float time;

float random(in float x) {
  return fract(sin(x) * 1e4);
}
float random(vec2 st) {
  return fract(sin(dot(st.xy, vec2(12.9898, 78.233))) * 43758.5453123);
}

void main(void) {
  vec2 uv = (gl_FragCoord.xy * 2.0 - resolution.xy) / min(resolution.x, resolution.y);

  vec2 fMosaicScal = vec2(4.0, 2.0);
  vec2 vScreenSize = vec2(256.0, 256.0);
  uv.x = floor(uv.x * vScreenSize.x / fMosaicScal.x) / (vScreenSize.x / fMosaicScal.x);
  uv.y = floor(uv.y * vScreenSize.y / fMosaicScal.y) / (vScreenSize.y / fMosaicScal.y);

  float t = time * 0.06 + random(uv.x) * 0.4;
  float lineWidth = 0.0008;

  vec3 color = vec3(0.0);
  for (int j = 0; j < 3; j++) {
    for (int i = 0; i < 5; i++) {
      color[j] += lineWidth * float(i * i) / abs(fract(t - 0.01 * float(j) + float(i) * 0.01) * 1.0 - length(uv));
    }
  }

  // Argus brand tint: recolor the three phase channels as amber → warm red → crimson
  // instead of blue/green/red. Keeps the multi-phase shimmer, changes only the palette.
  vec3 amber   = vec3(0.96, 0.65, 0.14); // #F5A623 — brand primary
  vec3 warmRed = vec3(0.90, 0.28, 0.14); // #E64724 — mid
  vec3 crimson = vec3(0.48, 0.12, 0.12); // #7a1f1f — deep
  vec3 tinted  = amber * color[0] + warmRed * color[1] + crimson * color[2];

  gl_FragColor = vec4(tinted, 1.0);
}
`;

export function ShaderAnimation({ className }: { className?: string }) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const node = containerRef.current;
    if (!node) return;

    const reduceMotion =
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const renderer = new Renderer({ dpr: window.devicePixelRatio });
    const gl = renderer.gl;
    gl.clearColor(0, 0, 0, 1);

    const uniforms = {
      time: { value: 1.0 },
      resolution: { value: new Vec2(1, 1) },
    };

    const program = new Program(gl, {
      vertex: VERT_SHADER,
      fragment: FRAG_SHADER,
      uniforms,
    });
    const mesh = new Mesh(gl, { geometry: new Triangle(gl), program });

    const resize = () => {
      renderer.setSize(node.offsetWidth, node.offsetHeight);
      uniforms.resolution.value.set(gl.canvas.width, gl.canvas.height);
      // In reduced-motion mode the rAF loop is skipped, so the canvas would show a stale
      // (or fully-cleared) frame at the new dimensions after a resize/orientation change.
      // Re-render once here so the panel never goes blank.
      if (reduceMotion) renderer.render({ scene: mesh });
    };
    resize();
    window.addEventListener("resize", resize, false);

    node.appendChild(gl.canvas);

    let rafId = 0;
    const tick = () => {
      uniforms.time.value += 0.05;
      renderer.render({ scene: mesh });
      rafId = requestAnimationFrame(tick);
    };

    if (reduceMotion) {
      // One static frame so the surface isn't black.
      renderer.render({ scene: mesh });
    } else {
      rafId = requestAnimationFrame(tick);
    }

    return () => {
      cancelAnimationFrame(rafId);
      window.removeEventListener("resize", resize);
      if (gl.canvas.parentNode === node) node.removeChild(gl.canvas);
      gl.getExtension("WEBGL_lose_context")?.loseContext();
    };
  }, []);

  return (
    <div
      ref={containerRef}
      aria-hidden="true"
      className={className}
      style={{ width: "100%", height: "100%", position: "absolute", inset: 0, overflow: "hidden" }}
    />
  );
}
