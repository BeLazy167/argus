/** Returns tailwind color class for a review score (0-10). */
export function scoreColor(score: number): string {
  if (score >= 8) return "text-green-400";
  if (score >= 5) return "text-amber";
  return "text-red-400";
}
