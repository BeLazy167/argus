"use client";

export function EyeSymbol({ className = "" }: { className?: string }) {
  return (
    <svg
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
        cx="60"
        cy="30"
        r="14"
        stroke="currentColor"
        strokeWidth="2"
        fill="none"
        opacity="0"
        style={{ animation: "fadeIn 0.5s ease-in 0.8s forwards" }}
      />
      {/* Diff marker pupil: < > */}
      <text
        x="60"
        y="34"
        textAnchor="middle"
        fill="currentColor"
        fontSize="14"
        fontFamily="JetBrains Mono, monospace"
        fontWeight="500"
        opacity="0"
        style={{ animation: "fadeIn 0.4s ease-in 1.2s forwards" }}
      >
        &lt;&gt;
      </text>
    </svg>
  );
}
