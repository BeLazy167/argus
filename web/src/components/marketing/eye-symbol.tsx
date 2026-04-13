"use client";

import { useEffect, useRef, useState, useCallback } from "react";

interface EyeSymbolProps {
  className?: string;
  trackMouse?: boolean;
}

export function EyeSymbol({ className = "", trackMouse = false }: EyeSymbolProps) {
  const svgRef = useRef<SVGSVGElement>(null);
  const [offset, setOffset] = useState({ x: 0, y: 0 });

  const handleMouseMove = useCallback((e: MouseEvent) => {
    const svg = svgRef.current;
    if (!svg) return;

    const rect = svg.getBoundingClientRect();
    const cx = rect.left + rect.width / 2;
    const cy = rect.top + rect.height / 2;

    const dx = e.clientX - cx;
    const dy = e.clientY - cy;
    const dist = Math.sqrt(dx * dx + dy * dy) || 1;

    const maxOff = 6;
    const norm = Math.min(dist / 300, 1);
    setOffset({ x: (dx / dist) * maxOff * norm, y: (dy / dist) * maxOff * norm });
  }, []);

  useEffect(() => {
    if (!trackMouse) return;
    window.addEventListener("mousemove", handleMouseMove);
    return () => window.removeEventListener("mousemove", handleMouseMove);
  }, [trackMouse, handleMouseMove]);

  return (
    <svg
      ref={svgRef}
      viewBox="0 0 120 60"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-hidden="true"
    >
      {/* Outer eye shape — draw-in */}
      <path
        d="M10 30C10 30 30 8 60 8C90 8 110 30 110 30C110 30 90 52 60 52C30 52 10 30 10 30Z"
        stroke="currentColor"
        strokeWidth="2"
        fill="none"
        strokeDasharray="300"
        strokeDashoffset="300"
        className="eye-outline"
      />
      {/* Iris — use transform for smooth tracking */}
      <g
        className="eye-iris"
        style={{ transform: `translate(${offset.x}px, ${offset.y}px)` }}
      >
        <circle
          cx="60"
          cy="30"
          r="14"
          stroke="currentColor"
          strokeWidth="2"
          fill="none"
        />
      </g>
      {/* Inner pupil */}
      {trackMouse && (
        <g
          className="eye-pupil"
          style={{ transform: `translate(${offset.x * 1.2}px, ${offset.y * 1.2}px)` }}
        >
          <circle cx="60" cy="30" r="4" fill="currentColor" />
        </g>
      )}
      {/* Diff marker */}
      <g
        className="eye-marker"
        style={{ transform: `translate(${offset.x}px, ${offset.y}px)` }}
      >
        <text
          x="60"
          y="34"
          textAnchor="middle"
          fill="currentColor"
          fontSize="14"
          fontFamily="var(--font-mono)"
          fontWeight="500"
        >
          &lt;&gt;
        </text>
      </g>
    </svg>
  );
}
