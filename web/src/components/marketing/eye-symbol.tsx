"use client";

import { useEffect, useRef, useState, useCallback } from "react";

interface EyeSymbolProps {
  className?: string;
  /** Enable mouse tracking for the iris/pupil */
  trackMouse?: boolean;
}

export function EyeSymbol({ className = "", trackMouse = false }: EyeSymbolProps) {
  const svgRef = useRef<SVGSVGElement>(null);
  const [pupilOffset, setPupilOffset] = useState({ x: 0, y: 0 });

  const handleMouseMove = useCallback((e: MouseEvent) => {
    const svg = svgRef.current;
    if (!svg) return;

    const rect = svg.getBoundingClientRect();
    const centerX = rect.left + rect.width / 2;
    const centerY = rect.top + rect.height / 2;

    const dx = e.clientX - centerX;
    const dy = e.clientY - centerY;
    const distance = Math.sqrt(dx * dx + dy * dy);

    const maxOffset = 6;
    const normalizedDistance = Math.min(distance / 300, 1);
    const offsetX = (dx / (distance || 1)) * maxOffset * normalizedDistance;
    const offsetY = (dy / (distance || 1)) * maxOffset * normalizedDistance;

    setPupilOffset({ x: offsetX, y: offsetY });
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
      {/* Outer eye shape */}
      <path
        d="M10 30C10 30 30 8 60 8C90 8 110 30 110 30C110 30 90 52 60 52C30 52 10 30 10 30Z"
        stroke="currentColor"
        strokeWidth="2"
        fill="none"
        className="animate-[draw_1.5s_ease-in-out_forwards]"
        strokeDasharray="300"
        strokeDashoffset="300"
        style={{ animation: "draw 1.5s ease-in-out forwards" }}
      />
      {/* Iris circle */}
      <circle
        cx={60 + pupilOffset.x}
        cy={30 + pupilOffset.y}
        r="14"
        stroke="currentColor"
        strokeWidth="2"
        fill="none"
        opacity="0"
        style={{
          animation: "fadeIn 0.5s ease-in 0.8s forwards",
          transition: "cx 0.1s ease-out, cy 0.1s ease-out",
        }}
      />
      {/* Inner pupil dot */}
      {trackMouse && (
        <circle
          cx={60 + pupilOffset.x * 1.2}
          cy={30 + pupilOffset.y * 1.2}
          r="4"
          fill="currentColor"
          opacity="0"
          style={{
            animation: "fadeIn 0.4s ease-in 1.0s forwards",
            transition: "cx 0.1s ease-out, cy 0.1s ease-out",
          }}
        />
      )}
      {/* Diff marker pupil: < > */}
      <text
        x={60 + pupilOffset.x}
        y={34 + pupilOffset.y}
        textAnchor="middle"
        fill="currentColor"
        fontSize="14"
        fontFamily="JetBrains Mono, monospace"
        fontWeight="500"
        opacity="0"
        style={{
          animation: "fadeIn 0.4s ease-in 1.2s forwards",
          transition: "x 0.1s ease-out, y 0.1s ease-out",
        }}
      >
        &lt;&gt;
      </text>
    </svg>
  );
}
