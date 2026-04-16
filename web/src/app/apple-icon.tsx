import { ImageResponse } from "next/og";

export const runtime = "edge";

export const size = { width: 180, height: 180 };
export const contentType = "image/png";

// Edge-rendered apple-touch-icon. We redraw the logo procedurally because
// next/og can't read arbitrary PNGs from /public at edge runtime. Shape
// mirrors the new amber eye + git-branch mark used by /public/logo.png.
export default async function AppleIcon() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: "#1A1A1A",
          borderRadius: 32,
        }}
      >
        <svg viewBox="0 0 120 60" width="130" height="65">
          {/* Eye outline */}
          <path
            d="M10 30 C 10 30 30 8 60 8 C 90 8 110 30 110 30 C 110 30 90 52 60 52 C 30 52 10 30 10 30 Z"
            stroke="#F5A623"
            strokeWidth="3"
            fill="none"
          />
          {/* Git-branch mark inside the eye */}
          <line
            x1="56"
            y1="16"
            x2="56"
            y2="46"
            stroke="#F5A623"
            strokeWidth="2.5"
          />
          <circle cx="56" cy="16" r="3" fill="#F5A623" />
          <circle cx="56" cy="46" r="3" fill="#F5A623" />
          <circle cx="72" cy="30" r="3" fill="#F5A623" />
          <path
            d="M 56 26 C 58 26 72 26 72 30"
            stroke="#F5A623"
            strokeWidth="2"
            fill="none"
          />
        </svg>
      </div>
    ),
    { ...size },
  );
}
