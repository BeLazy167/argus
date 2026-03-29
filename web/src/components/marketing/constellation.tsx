// @ts-nocheck — Three.js typed array indexing produces false TS errors
"use client";

import { useRef, useMemo } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import * as THREE from "three";

const PARTICLE_COUNT = 80;
const CONNECTION_DISTANCE = 2.2;
const DRIFT_SPEED = 0.08;
const BOUNDS = { x: 8, y: 4.5, z: 2 };

/** Amber color from Argus palette — oklch(0.77 0.15 75) ≈ #d4952a */
const AMBER = new THREE.Color(0.83, 0.58, 0.16);
const AMBER_DIM = new THREE.Color(0.83, 0.58, 0.16).multiplyScalar(0.3);

function Particles() {
  const pointsRef = useRef<THREE.Points>(null);
  const linesRef = useRef<THREE.LineSegments>(null);

  // Initialize particle positions + velocities
  const { positions, velocities } = useMemo(() => {
    const pos = new Float32Array(PARTICLE_COUNT * 3);
    const vel = new Float32Array(PARTICLE_COUNT * 3);
    for (let i = 0; i < PARTICLE_COUNT; i++) {
      pos[i * 3] = (Math.random() - 0.5) * BOUNDS.x * 2;
      pos[i * 3 + 1] = (Math.random() - 0.5) * BOUNDS.y * 2;
      pos[i * 3 + 2] = (Math.random() - 0.5) * BOUNDS.z * 2;
      vel[i * 3] = (Math.random() - 0.5) * DRIFT_SPEED;
      vel[i * 3 + 1] = (Math.random() - 0.5) * DRIFT_SPEED;
      vel[i * 3 + 2] = (Math.random() - 0.5) * DRIFT_SPEED * 0.3;
    }
    return { positions: pos, velocities: vel };
  }, []);

  // Pre-allocate line geometry (max connections = PARTICLE_COUNT * 6)
  const maxLines = PARTICLE_COUNT * 6;
  const linePositions = useMemo(() => new Float32Array(maxLines * 6), [maxLines]);
  const lineColors = useMemo(() => new Float32Array(maxLines * 6), [maxLines]);

  // Point sizes — random for organic feel
  const sizes = useMemo(() => {
    const s = new Float32Array(PARTICLE_COUNT);
    for (let i = 0; i < PARTICLE_COUNT; i++) {
      s[i] = 1.5 + Math.random() * 2.5;
    }
    return s;
  }, []);

  useFrame((_, delta) => {
    if (!pointsRef.current || !linesRef.current) return;

    const posArray = positions;

    // Move particles
    for (let i = 0; i < PARTICLE_COUNT; i++) {
      const ix = i * 3;
      posArray[ix] += velocities[ix] * delta * 60;
      posArray[ix + 1] += velocities[ix + 1] * delta * 60;
      posArray[ix + 2] += velocities[ix + 2] * delta * 60;

      // Bounce off bounds
      if (Math.abs(posArray[ix]) > BOUNDS.x) velocities[ix] *= -1;
      if (Math.abs(posArray[ix + 1]) > BOUNDS.y) velocities[ix + 1] *= -1;
      if (Math.abs(posArray[ix + 2]) > BOUNDS.z) velocities[ix + 2] *= -1;
    }
    const posGeomAttr = pointsRef.current.geometry.getAttribute("position") as THREE.BufferAttribute;
    if (posGeomAttr) posGeomAttr.needsUpdate = true;

    // Build connection lines
    let lineIdx = 0;
    for (let i = 0; i < PARTICLE_COUNT; i++) {
      for (let j = i + 1; j < PARTICLE_COUNT; j++) {
        const dx = posArray[i * 3] - posArray[j * 3];
        const dy = posArray[i * 3 + 1] - posArray[j * 3 + 1];
        const dz = posArray[i * 3 + 2] - posArray[j * 3 + 2];
        const dist = Math.sqrt(dx * dx + dy * dy + dz * dz);

        if (dist < CONNECTION_DISTANCE && lineIdx < maxLines) {
          const alpha = 1 - dist / CONNECTION_DISTANCE;
          const offset = lineIdx * 6;

          linePositions[offset] = posArray[i * 3];
          linePositions[offset + 1] = posArray[i * 3 + 1];
          linePositions[offset + 2] = posArray[i * 3 + 2];
          linePositions[offset + 3] = posArray[j * 3];
          linePositions[offset + 4] = posArray[j * 3 + 1];
          linePositions[offset + 5] = posArray[j * 3 + 2];

          // Fade line color by distance
          lineColors[offset] = AMBER_DIM.r * alpha;
          lineColors[offset + 1] = AMBER_DIM.g * alpha;
          lineColors[offset + 2] = AMBER_DIM.b * alpha;
          lineColors[offset + 3] = AMBER_DIM.r * alpha;
          lineColors[offset + 4] = AMBER_DIM.g * alpha;
          lineColors[offset + 5] = AMBER_DIM.b * alpha;

          lineIdx++;
        }
      }
    }

    // Zero out remaining lines
    for (let i = lineIdx * 6; i < linePositions.length; i++) {
      linePositions[i] = 0;
      lineColors[i] = 0;
    }

    const lineGeom = linesRef.current.geometry;
    const linePosAttr = lineGeom.getAttribute("position") as THREE.BufferAttribute;
    const lineColAttr = lineGeom.getAttribute("color") as THREE.BufferAttribute;
    if (linePosAttr) linePosAttr.needsUpdate = true;
    if (lineColAttr) lineColAttr.needsUpdate = true;
    lineGeom.setDrawRange(0, lineIdx * 2);
  });

  return (
    <>
      {/* Particles */}
      <points ref={pointsRef}>
        <bufferGeometry>
          <bufferAttribute
            attach="attributes-position"
            array={positions}
            count={PARTICLE_COUNT}
            itemSize={3}
          />
          <bufferAttribute
            attach="attributes-size"
            array={sizes}
            count={PARTICLE_COUNT}
            itemSize={1}
          />
        </bufferGeometry>
        <pointsMaterial
          size={2}
          color={AMBER}
          transparent
          opacity={0.6}
          sizeAttenuation
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </points>

      {/* Connection lines */}
      <lineSegments ref={linesRef}>
        <bufferGeometry>
          <bufferAttribute
            attach="attributes-position"
            array={linePositions}
            count={maxLines * 2}
            itemSize={3}
          />
          <bufferAttribute
            attach="attributes-color"
            array={lineColors}
            count={maxLines * 2}
            itemSize={3}
          />
        </bufferGeometry>
        <lineBasicMaterial
          vertexColors
          transparent
          opacity={0.4}
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </lineSegments>
    </>
  );
}

export function ConstellationBackground() {
  return (
    <div className="absolute inset-0 -z-10 opacity-40">
      <Canvas
        camera={{ position: [0, 0, 6], fov: 60 }}
        dpr={[1, 1.5]}
        gl={{ antialias: false, alpha: true }}
        style={{ background: "transparent" }}
      >
        <Particles />
      </Canvas>
    </div>
  );
}
