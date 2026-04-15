import { create } from "zustand";
import type { Period } from "@/lib/queries/org-stats";

interface StatsState {
  period: Period;
  setPeriod: (p: Period) => void;
}

export const useStatsStore = create<StatsState>((set) => ({
  period: "30d",
  setPeriod: (period) => set({ period }),
}));
