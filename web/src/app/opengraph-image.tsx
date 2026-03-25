import { ImageResponse } from "next/og";

export const runtime = "edge";

export const alt = "ARGUS — AI Code Review That Builds Institutional Memory";
export const size = { width: 1200, height: 630 };
export const contentType = "image/png";

export default async function OGImage() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: "#1A1A1A",
          position: "relative",
        }}
      >
        {/* Subtle gradient glow */}
        <div
          style={{
            position: "absolute",
            top: "50%",
            left: "50%",
            transform: "translate(-50%, -50%)",
            width: 600,
            height: 600,
            borderRadius: "50%",
            background:
              "radial-gradient(circle, rgba(245,166,35,0.15) 0%, transparent 70%)",
          }}
        />

        {/* Eye symbol */}
        <svg
          viewBox="0 0 120 60"
          width="160"
          height="80"
          style={{ marginBottom: 24 }}
        >
          <path
            d="M10 30C10 30 30 8 60 8C90 8 110 30 110 30C110 30 90 52 60 52C30 52 10 30 10 30Z"
            stroke="#F5A623"
            strokeWidth="3"
            fill="none"
          />
          <circle
            cx="60"
            cy="30"
            r="14"
            stroke="#F5A623"
            strokeWidth="2.5"
            fill="none"
          />
          <circle cx="60" cy="30" r="4" fill="#F5A623" />
        </svg>

        {/* Wordmark */}
        <div
          style={{
            fontSize: 72,
            fontWeight: 700,
            color: "#F5F5F5",
            letterSpacing: "0.15em",
            marginBottom: 16,
            fontFamily: "sans-serif",
          }}
        >
          ARGUS
        </div>

        {/* Tagline */}
        <div
          style={{
            fontSize: 28,
            color: "#F5A623",
            marginBottom: 12,
            fontStyle: "italic",
            fontFamily: "sans-serif",
          }}
        >
          Nothing merges unseen.
        </div>

        {/* Subtitle */}
        <div
          style={{
            fontSize: 18,
            color: "#999999",
            fontFamily: "monospace",
          }}
        >
          AI code review that builds institutional memory
        </div>
      </div>
    ),
    { ...size }
  );
}
