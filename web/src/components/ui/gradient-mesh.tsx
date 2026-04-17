"use client";

/**
 * Animated WebGL gradient mesh.
 *
 * Adapted from @designali-in/gradient-mesh for the Argus sign-up panel. Uses OGL (a slim WebGL
 * wrapper, ~20KB gzipped — much lighter than three.js) so the left auth panel stays responsive.
 *
 * Respects `prefers-reduced-motion`: when set, the animation loop is paused and the shader
 * renders a static frame. The `aria-hidden` root means screen readers ignore the canvas entirely.
 */
import { Renderer, Program, Mesh, Color, Triangle } from "ogl";
import { useEffect, useRef } from "react";

type GradientMeshProps = {
  /** Three hex colors (`#rrggbb`) sampled by the shader. Only the first three are used. */
  colors?: string[];
  /** Higher = more fractal detail. 3-8 is the sweet spot. */
  distortion?: number;
  /** Polar swirl amount, 0 disables the vortex. */
  swirl?: number;
  /** Animation speed multiplier. 0 = static. */
  speed?: number;
  scale?: number;
  offsetX?: number;
  offsetY?: number;
  /** Rotation in radians. */
  rotation?: number;
  waveAmp?: number;
  waveFreq?: number;
  waveSpeed?: number;
  /** Film-grain intensity, 0-0.2. */
  grain?: number;
  className?: string;
};

const VERT_SHADER = `
attribute vec2 uv;
attribute vec2 position;
varying vec2 vUv;
void main() {
  vUv = uv;
  gl_Position = vec4(position, 0.0, 1.0);
}
`;

const fragShader = (distortion: number) => `
precision highp float;

uniform float uTime;
uniform float uSwirl;
uniform float uSpeed;
uniform float uScale;
uniform float uOffsetX;
uniform float uOffsetY;
uniform float uRotation;
uniform float uWaveAmp;
uniform float uWaveFreq;
uniform float uWaveSpeed;
uniform float uGrain;
uniform vec3 uColorA;
uniform vec3 uColorB;
uniform vec3 uColorC;
uniform vec3 uResolution;

varying vec2 vUv;

float wave(vec2 uv, float freq, float speed, float time) {
  return sin(uv.x * freq + time * speed) * cos(uv.y * freq + time * speed);
}

float rand(vec2 st) {
  return fract(sin(dot(st.xy, vec2(12.9898, 78.233))) * 43758.5453123);
}

vec3 colorDodge(vec3 base, vec3 blend) {
  return min(base / (1.0 - blend + 0.0001), 1.0);
}

void main() {
  float mr = min(uResolution.x, uResolution.y);
  vec2 uv = (vUv.xy * 2.0 - 1.0) * uResolution.xy / mr;
  uv = uv * uScale + vec2(uOffsetX, uOffsetY);

  float cosR = cos(uRotation);
  float sinR = sin(uRotation);
  uv = vec2(uv.x * cosR - uv.y * sinR, uv.x * sinR + uv.y * cosR);

  uv.x += wave(uv, uWaveFreq, uWaveSpeed, uTime) * uWaveAmp;
  uv.y += wave(uv + 10.0, uWaveFreq * 1.5, uWaveSpeed * 0.8, uTime) * uWaveAmp * 0.5;

  float angle = atan(uv.y, uv.x);
  float radius = length(uv);
  angle += uSwirl * radius;
  uv = vec2(cos(angle), sin(angle)) * radius;

  float d = -uTime * 0.5 * uSpeed;
  float a = 0.0;
  for (float i = 0.0; i < ${distortion.toFixed(1)}; ++i) {
    a += cos(i - d - a * uv.x);
    d += sin(uv.y * i + a);
  }
  d += uTime * 0.5 * uSpeed;

  float mix1 = (sin(d) + 1.0) * 0.5;
  float mix2 = (cos(a) + 1.0) * 0.5;
  vec3 col = mix(uColorA, uColorB, mix1);
  col = mix(col, uColorC, mix2);

  float grain = (rand(gl_FragCoord.xy + uTime) - 0.5) * uGrain;
  col = colorDodge(col, vec3(0.5 + grain));

  gl_FragColor = vec4(col, 1.0);
}
`;

function hexToRgb(hex: string): [number, number, number] {
  const clean = hex.replace("#", "");
  const r = parseInt(clean.substring(0, 2), 16) / 255;
  const g = parseInt(clean.substring(2, 4), 16) / 255;
  const b = parseInt(clean.substring(4, 6), 16) / 255;
  return [r, g, b];
}

export function GradientMesh({
  colors = ["#F5A623", "#7a1f1f", "#0a0612"],
  distortion = 5,
  swirl = 0.3,
  speed = 0.6,
  scale = 1,
  offsetX = 0,
  offsetY = 0,
  rotation = 90,
  waveAmp = 0.15,
  waveFreq = 14,
  waveSpeed = 0.2,
  grain = 0.05,
  className,
}: GradientMeshProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const node = containerRef.current;
    if (!node) return;

    const reduceMotion =
      typeof window !== "undefined" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const renderer = new Renderer();
    const gl = renderer.gl;
    gl.clearColor(0, 0, 0, 1);

    const resize = () => {
      renderer.setSize(node.offsetWidth, node.offsetHeight);
      uniforms.uResolution.value = new Color(
        gl.canvas.width,
        gl.canvas.height,
        gl.canvas.width / Math.max(1, gl.canvas.height),
      );
    };

    const FALLBACK: [number, number, number] = [0, 0, 0];
    const [rgbA, rgbB, rgbC] = [colors[0], colors[1], colors[2]].map((c) =>
      c ? hexToRgb(c) : FALLBACK,
    ) as [[number, number, number], [number, number, number], [number, number, number]];
    const uniforms = {
      uTime: { value: 0 },
      uSwirl: { value: swirl },
      uSpeed: { value: speed },
      uScale: { value: scale },
      uOffsetX: { value: offsetX },
      uOffsetY: { value: offsetY },
      uRotation: { value: rotation },
      uWaveAmp: { value: waveAmp },
      uWaveFreq: { value: waveFreq },
      uWaveSpeed: { value: waveSpeed },
      uGrain: { value: grain },
      uResolution: { value: new Color(1, 1, 1) },
      uColorA: { value: new Color(...rgbA) },
      uColorB: { value: new Color(...rgbB) },
      uColorC: { value: new Color(...rgbC) },
    };

    resize();
    window.addEventListener("resize", resize, false);

    const program = new Program(gl, {
      vertex: VERT_SHADER,
      fragment: fragShader(distortion),
      uniforms,
    });
    const mesh = new Mesh(gl, { geometry: new Triangle(gl), program });

    node.appendChild(gl.canvas);

    let rafId = 0;
    const tick = (t: number) => {
      uniforms.uTime.value = t * 0.001;
      renderer.render({ scene: mesh });
      if (!reduceMotion) rafId = requestAnimationFrame(tick);
    };

    if (reduceMotion) {
      // Draw one static frame so the panel isn't black.
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
  }, [colors, distortion, swirl, speed, scale, offsetX, offsetY, rotation, waveAmp, waveFreq, waveSpeed, grain]);

  return (
    <div
      ref={containerRef}
      aria-hidden="true"
      className={className}
      style={{ width: "100%", height: "100%", position: "absolute", inset: 0, overflow: "hidden" }}
    />
  );
}
