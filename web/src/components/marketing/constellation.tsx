// @ts-nocheck — Three.js typed array indexing produces false TS errors
"use client";

import { useRef, useMemo, useCallback } from "react";
import { Canvas, useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";

/* ── Constants ── */
const RING_COUNT = 3;
const NODES_PER_RING = [12, 18, 24];
const RING_RADII = [2.2, 3.8, 5.5];
const RING_SPEEDS = [0.12, -0.08, 0.05];
const CONNECTION_DISTANCE = 2.0;
const ORBIT_TILT = [0.3, -0.15, 0.1];

const AMBER = new THREE.Color(0.83, 0.58, 0.16);
const AMBER_BRIGHT = new THREE.Color(1.0, 0.72, 0.2);
const AMBER_DIM = new THREE.Color(0.83, 0.58, 0.16).multiplyScalar(0.25);

/** Soft circular particle texture */
function createCircleTexture(): THREE.Texture {
  const size = 64;
  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext("2d");
  if (ctx) {
    const gradient = ctx.createRadialGradient(
      size / 2, size / 2, 0,
      size / 2, size / 2, size / 2
    );
    gradient.addColorStop(0, "rgba(255,255,255,1)");
    gradient.addColorStop(0.3, "rgba(255,255,255,0.8)");
    gradient.addColorStop(1, "rgba(255,255,255,0)");
    ctx.fillStyle = gradient;
    ctx.fillRect(0, 0, size, size);
  }
  const texture = new THREE.CanvasTexture(canvas);
  texture.needsUpdate = true;
  return texture;
}

/** Glow ring texture for the iris */
function createRingGlowTexture(): THREE.Texture {
  const size = 128;
  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const ctx = canvas.getContext("2d");
  if (ctx) {
    const gradient = ctx.createRadialGradient(
      size / 2, size / 2, size * 0.3,
      size / 2, size / 2, size / 2
    );
    gradient.addColorStop(0, "rgba(212,149,42,0)");
    gradient.addColorStop(0.7, "rgba(212,149,42,0.15)");
    gradient.addColorStop(0.85, "rgba(212,149,42,0.4)");
    gradient.addColorStop(1, "rgba(212,149,42,0)");
    ctx.fillStyle = gradient;
    ctx.fillRect(0, 0, size, size);
  }
  const texture = new THREE.CanvasTexture(canvas);
  texture.needsUpdate = true;
  return texture;
}

/* ── Central Eye Iris ── */
function EyeIris() {
  const irisRef = useRef<THREE.Group>(null);
  const innerRef = useRef<THREE.Mesh>(null);
  const ringGlowTexture = useMemo(() => createRingGlowTexture(), []);

  useFrame((state) => {
    if (!irisRef.current || !innerRef.current) return;
    const t = state.clock.elapsedTime;

    // Subtle breathing pulse on the iris
    const scale = 1 + Math.sin(t * 1.5) * 0.06;
    irisRef.current.scale.setScalar(scale);

    // Inner pupil glow modulation
    const mat = innerRef.current.material as THREE.MeshBasicMaterial;
    mat.opacity = 0.6 + Math.sin(t * 2.0) * 0.2;
  });

  return (
    <group ref={irisRef}>
      {/* Outer iris ring */}
      <mesh rotation={[0, 0, 0]}>
        <ringGeometry args={[0.8, 1.1, 64]} />
        <meshBasicMaterial
          color={AMBER}
          transparent
          opacity={0.5}
          side={THREE.DoubleSide}
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </mesh>

      {/* Inner pupil */}
      <mesh ref={innerRef}>
        <circleGeometry args={[0.45, 48]} />
        <meshBasicMaterial
          color={AMBER_BRIGHT}
          transparent
          opacity={0.7}
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </mesh>

      {/* Glow halo */}
      <sprite scale={[5, 5, 1]}>
        <spriteMaterial
          map={ringGlowTexture}
          transparent
          opacity={0.4}
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </sprite>
    </group>
  );
}

/* ── Orbiting Nodes (code/dependency representation) ── */
function OrbitingNodes() {
  const pointsRef = useRef<THREE.Points>(null);
  const linesRef = useRef<THREE.LineSegments>(null);
  const circleTexture = useMemo(() => createCircleTexture(), []);

  const totalNodes = useMemo(() => NODES_PER_RING.reduce((a, b) => a + b, 0), []);

  // Pre-compute initial angles for each node
  const { angles, ringIndices } = useMemo(() => {
    const angs: number[] = [];
    const rIndices: number[] = [];
    for (let r = 0; r < RING_COUNT; r++) {
      const count = NODES_PER_RING[r];
      for (let i = 0; i < count; i++) {
        angs.push((i / count) * Math.PI * 2 + Math.random() * 0.3);
        rIndices.push(r);
      }
    }
    return { angles: angs, ringIndices: rIndices };
  }, []);

  const positions = useMemo(() => new Float32Array(totalNodes * 3), [totalNodes]);
  const sizes = useMemo(() => {
    const s = new Float32Array(totalNodes);
    for (let i = 0; i < totalNodes; i++) {
      s[i] = 1.5 + Math.random() * 2.5;
    }
    return s;
  }, [totalNodes]);

  // Pre-allocate line geometry
  const maxLines = totalNodes * 4;
  const linePositions = useMemo(() => new Float32Array(maxLines * 6), [maxLines]);
  const lineColors = useMemo(() => new Float32Array(maxLines * 6), [maxLines]);

  // Ambient scatter particles (small dust)
  const dustCount = 40;
  const dustRef = useRef<THREE.Points>(null);
  const dustPositions = useMemo(() => {
    const p = new Float32Array(dustCount * 3);
    for (let i = 0; i < dustCount; i++) {
      const theta = Math.random() * Math.PI * 2;
      const phi = Math.acos(2 * Math.random() - 1);
      const r = 3 + Math.random() * 5;
      p[i * 3] = r * Math.sin(phi) * Math.cos(theta);
      p[i * 3 + 1] = r * Math.sin(phi) * Math.sin(theta) * 0.6;
      p[i * 3 + 2] = r * Math.cos(phi) * 0.3;
    }
    return p;
  }, []);

  useFrame((state, delta) => {
    if (!pointsRef.current || !linesRef.current) return;
    const t = state.clock.elapsedTime;

    // Update node positions along orbits
    for (let i = 0; i < totalNodes; i++) {
      const r = ringIndices[i];
      const radius = RING_RADII[r];
      const speed = RING_SPEEDS[r];
      const tilt = ORBIT_TILT[r];
      const angle = angles[i] + t * speed;

      // Elliptical orbit with tilt
      const x = Math.cos(angle) * radius;
      const y = Math.sin(angle) * radius * 0.55 + Math.sin(angle + tilt) * radius * 0.1;
      const z = Math.sin(angle * 0.5 + tilt) * 0.8;

      positions[i * 3] = x;
      positions[i * 3 + 1] = y;
      positions[i * 3 + 2] = z;
    }

    const posAttr = pointsRef.current.geometry.getAttribute("position") as THREE.BufferAttribute;
    if (posAttr) posAttr.needsUpdate = true;

    // Build connection lines between nearby nodes
    let lineIdx = 0;
    for (let i = 0; i < totalNodes; i++) {
      for (let j = i + 1; j < totalNodes; j++) {
        const dx = positions[i * 3] - positions[j * 3];
        const dy = positions[i * 3 + 1] - positions[j * 3 + 1];
        const dz = positions[i * 3 + 2] - positions[j * 3 + 2];
        const dist = Math.sqrt(dx * dx + dy * dy + dz * dz);

        if (dist < CONNECTION_DISTANCE && lineIdx < maxLines) {
          const alpha = (1 - dist / CONNECTION_DISTANCE) * 0.6;
          const offset = lineIdx * 6;

          linePositions[offset] = positions[i * 3];
          linePositions[offset + 1] = positions[i * 3 + 1];
          linePositions[offset + 2] = positions[i * 3 + 2];
          linePositions[offset + 3] = positions[j * 3];
          linePositions[offset + 4] = positions[j * 3 + 1];
          linePositions[offset + 5] = positions[j * 3 + 2];

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

    // Clear remaining
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

    // Rotate dust slowly
    if (dustRef.current) {
      dustRef.current.rotation.y += delta * 0.015;
      dustRef.current.rotation.x = Math.sin(t * 0.1) * 0.05;
    }
  });

  return (
    <>
      {/* Orbiting code nodes */}
      <points ref={pointsRef}>
        <bufferGeometry>
          <bufferAttribute
            attach="attributes-position"
            array={positions}
            count={totalNodes}
            itemSize={3}
          />
          <bufferAttribute
            attach="attributes-size"
            array={sizes}
            count={totalNodes}
            itemSize={1}
          />
        </bufferGeometry>
        <pointsMaterial
          size={0.1}
          color={AMBER}
          map={circleTexture}
          transparent
          opacity={0.8}
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
          opacity={0.3}
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </lineSegments>

      {/* Ambient dust */}
      <points ref={dustRef}>
        <bufferGeometry>
          <bufferAttribute
            attach="attributes-position"
            array={dustPositions}
            count={dustCount}
            itemSize={3}
          />
        </bufferGeometry>
        <pointsMaterial
          size={0.04}
          color={AMBER}
          map={circleTexture}
          transparent
          opacity={0.3}
          sizeAttenuation
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </points>
    </>
  );
}

/* ── Scan Rings ── */
function ScanRings() {
  const ring1Ref = useRef<THREE.Mesh>(null);
  const ring2Ref = useRef<THREE.Mesh>(null);

  useFrame((state) => {
    const t = state.clock.elapsedTime;

    if (ring1Ref.current) {
      ring1Ref.current.rotation.z = t * 0.3;
      const mat = ring1Ref.current.material as THREE.MeshBasicMaterial;
      mat.opacity = 0.08 + Math.sin(t * 0.8) * 0.04;
    }
    if (ring2Ref.current) {
      ring2Ref.current.rotation.z = -t * 0.2;
      const mat = ring2Ref.current.material as THREE.MeshBasicMaterial;
      mat.opacity = 0.06 + Math.cos(t * 1.2) * 0.03;
    }
  });

  return (
    <>
      <mesh ref={ring1Ref} rotation={[Math.PI / 2, 0, 0]}>
        <ringGeometry args={[3.5, 3.55, 128]} />
        <meshBasicMaterial
          color={AMBER}
          transparent
          opacity={0.1}
          side={THREE.DoubleSide}
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </mesh>
      <mesh ref={ring2Ref} rotation={[Math.PI / 2, 0.4, 0]}>
        <ringGeometry args={[5.0, 5.04, 128]} />
        <meshBasicMaterial
          color={AMBER}
          transparent
          opacity={0.06}
          side={THREE.DoubleSide}
          depthWrite={false}
          blending={THREE.AdditiveBlending}
        />
      </mesh>
    </>
  );
}

/* ── Responsive Camera ── */
function ResponsiveCamera() {
  const { camera, viewport } = useThree();

  useFrame(() => {
    // Pull camera back on narrow screens for full view
    const targetZ = viewport.width < 6 ? 9 : 7;
    camera.position.z += (targetZ - camera.position.z) * 0.05;
  });

  return null;
}

/* ── Scene ── */
function ArgusScene() {
  const groupRef = useRef<THREE.Group>(null);

  useFrame((state) => {
    if (!groupRef.current) return;
    const t = state.clock.elapsedTime;
    // Gentle floating motion
    groupRef.current.rotation.x = Math.sin(t * 0.15) * 0.05;
    groupRef.current.rotation.y = Math.sin(t * 0.1) * 0.03;
    groupRef.current.position.y = Math.sin(t * 0.2) * 0.15;
  });

  return (
    <group ref={groupRef}>
      <OrbitingNodes />
      <ScanRings />
      <ResponsiveCamera />
    </group>
  );
}

export function ConstellationBackground() {
  return (
    <div className="absolute inset-0 -z-10 opacity-70">
      <Canvas
        camera={{ position: [0, 0, 7], fov: 60 }}
        dpr={[1, 1.5]}
        gl={{ antialias: false, alpha: true }}
        style={{ background: "transparent" }}
      >
        <ArgusScene />
      </Canvas>
    </div>
  );
}
