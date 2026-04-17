"use client";

import { useEffect, useRef } from "react";
import * as THREE from "three";
import { EffectComposer } from "three/examples/jsm/postprocessing/EffectComposer.js";
import { RenderPass } from "three/examples/jsm/postprocessing/RenderPass.js";
import { ShaderPass } from "three/examples/jsm/postprocessing/ShaderPass.js";
import { UnrealBloomPass } from "three/examples/jsm/postprocessing/UnrealBloomPass.js";
import { RGBShiftShader } from "three/examples/jsm/shaders/RGBShiftShader.js";

/**
 * AiHeroBackground — THREE.js instanced dot grid with bloom + subtle RGB shift,
 * animated as a radial rounded-square wave pulse. Reads as "signal rippling
 * through a mesh" — a good metaphor for review scenarios propagating across
 * files.
 *
 * Adapted from the donor component for Argus:
 *   - dots use `--color-amber-glow` instead of white
 *   - canvas clears to transparent so the parent section's background shows
 *     through (the donor set an opaque black which clobbered the layout)
 *   - grid density halved (60x60 vs 120x120) to keep mobile GPUs happy;
 *     the pulse is still continuous, just with a coarser mesh
 *   - bloom intensity dialed back so the effect is ambient, not the star
 *
 * SSR-safe: useEffect gates all THREE work to the client, WebGLRenderer is
 * created on mount, torn down cleanly on unmount (observer, RAF, geometry,
 * material, renderer, composer all disposed).
 */
export function AiHeroBackground() {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const container = containerRef.current;
    if (!container) return;

    // Clear any prior canvas if this runs twice (StrictMode / hot reload).
    while (container.firstChild) container.removeChild(container.firstChild);

    // Resolve brand color from CSS custom property. Falls back to amber hex
    // if the custom property isn't parseable at render time (SSR edge cases,
    // themes mid-swap).
    const resolvedAmber =
      getComputedStyle(document.documentElement)
        .getPropertyValue("--color-amber-glow")
        .trim() || "#f5a623";
    const dotColor = new THREE.Color(resolvedAmber);

    const renderer = new THREE.WebGLRenderer({
      antialias: false,
      alpha: true,
      powerPreference: "high-performance",
    });
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    renderer.setSize(container.clientWidth, container.clientHeight);
    renderer.setClearColor(0x000000, 0);
    container.appendChild(renderer.domElement);

    const scene = new THREE.Scene();
    const camera = new THREE.OrthographicCamera();

    const renderPass = new RenderPass(scene, camera);
    const bloom = new UnrealBloomPass(
      new THREE.Vector2(container.clientWidth, container.clientHeight),
      0.25, // strength
      0.9,  // radius
      0.2,  // threshold
    );
    const rgbShift = new ShaderPass(RGBShiftShader);
    rgbShift.uniforms.amount!.value = 0.0012;
    rgbShift.uniforms.angle!.value = Math.PI / 4;

    const composer = new EffectComposer(renderer);
    composer.addPass(renderPass);
    composer.addPass(bloom);
    composer.addPass(rgbShift);

    const GRID = {
      cols: 60,
      rows: 60,
      jitter: 0.3,
      hexOffset: 0.5,
      dotRadius: 0.04,
      spacing: 0.6,
    };
    const total = GRID.cols * GRID.rows;
    const geometry = new THREE.CircleGeometry(GRID.dotRadius, 8);
    const material = new THREE.MeshBasicMaterial({ color: dotColor });
    const dots = new THREE.InstancedMesh(geometry, material, total);
    dots.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
    scene.add(dots);

    const basePos = new Float32Array(total * 2);
    const distArr = new Float32Array(total);
    const xOffset = (GRID.cols - 1) * GRID.spacing * 0.5;
    const yOffset = (GRID.rows - 1) * GRID.spacing * 0.5;
    const dummy = new THREE.Object3D();

    let idx = 0;
    for (let r = 0; r < GRID.rows; r++) {
      for (let c = 0; c < GRID.cols; c++, idx++) {
        let x = c * GRID.spacing - xOffset;
        let y = r * GRID.spacing - yOffset;
        y += (c % 2) * GRID.hexOffset * GRID.spacing;
        x += (Math.random() - 0.5) * GRID.jitter;
        y += (Math.random() - 0.5) * GRID.jitter;
        basePos[idx * 2] = x;
        basePos[idx * 2 + 1] = y;
        const len = Math.hypot(x, y);
        const ang = Math.atan2(y, x);
        const oct = 0.5 * Math.cos(ang * 8.0);
        distArr[idx] = len + oct * 0.75;
        dummy.position.set(x, y, 0);
        dummy.updateMatrix();
        dots.setMatrixAt(idx, dummy.matrix);
      }
    }

    // Rounded square wave used to make the radial pulse have visible crests
    // and troughs rather than pure sinusoidal glide. `delta` controls sharpness.
    const roundedSquareWave = (t: number, delta: number, a: number, f: number) =>
      ((2 * a) / Math.PI) * Math.atan(Math.sin(2 * Math.PI * t * f) / delta);

    const clock = new THREE.Clock();
    let rafId = 0;

    const animate = () => {
      rafId = requestAnimationFrame(animate);
      const t = clock.getElapsedTime();
      const speed = 0.5;
      const amp = 0.6; // slightly dialed back vs donor (0.75) for restraint
      const freq = 0.3;
      const falloff = 0.035;
      const phase = (Math.sin(2 * Math.PI * t * freq) + 1) * 0.5;
      rgbShift.uniforms.amount!.value = 0.0008 + phase * 0.0018;
      const mat = new THREE.Matrix4();
      const pos = new THREE.Vector3();
      for (let i = 0; i < total; i++) {
        const x0 = basePos[i * 2]!;
        const y0 = basePos[i * 2 + 1]!;
        const dist = distArr[i]!;
        const localDelta = THREE.MathUtils.lerp(0.05, 0.2, Math.min(1.0, dist / 70.0));
        const tt = t * speed - dist * falloff;
        const k = 1 + roundedSquareWave(tt, localDelta, amp, freq);
        pos.set(x0 * k, y0 * k, 0);
        mat.set(1, 0, 0, pos.x, 0, 1, 0, pos.y, 0, 0, 1, 0, 0, 0, 0, 1);
        dots.setMatrixAt(i, mat);
      }
      dots.instanceMatrix.needsUpdate = true;
      composer.render();
    };

    const resize = () => {
      const w = container.clientWidth;
      const h = container.clientHeight;
      if (!w || !h) return;
      const aspect = w / h;
      const worldHeight = 10;
      const worldWidth = worldHeight * aspect;
      camera.left = -worldWidth / 2;
      camera.right = worldWidth / 2;
      camera.top = worldHeight / 2;
      camera.bottom = -worldHeight / 2;
      camera.near = -100;
      camera.far = 100;
      camera.position.set(0, 0, 10);
      camera.updateProjectionMatrix();
      renderer.setSize(w, h);
      composer.setSize(w, h);
      if (rgbShift.uniforms.resolution) {
        rgbShift.uniforms.resolution.value.set(w, h);
      }
      bloom.setSize(w, h);
    };

    const observer = new ResizeObserver(resize);
    observer.observe(container);
    resize();
    animate();

    return () => {
      observer.disconnect();
      cancelAnimationFrame(rafId);
      while (container.firstChild) container.removeChild(container.firstChild);
      geometry.dispose();
      material.dispose();
      renderer.dispose();
      composer.dispose();
    };
  }, []);

  return (
    <div
      ref={containerRef}
      aria-hidden="true"
      className="pointer-events-none absolute inset-0 z-0"
    />
  );
}
