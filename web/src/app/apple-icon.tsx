import { ImageResponse } from "next/og";

export const runtime = "edge";

export const size = { width: 180, height: 180 };
export const contentType = "image/png";

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
        <svg viewBox="0 0 120 60" width="120" height="60">
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
      </div>
    ),
    { ...size }
  );
}
