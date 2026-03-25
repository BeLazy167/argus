"use client";
import { createContext, useContext, useState, useMemo, type ReactNode } from "react";
import { useRepos } from "@/lib/queries/repos";
import type { Repo } from "@/lib/types";

type ActiveRepoContextType = {
  repos: Repo[];
  activeId: number;
  setSelectedId: (id: number) => void;
  isLoading: boolean;
};

const ActiveRepoContext = createContext<ActiveRepoContextType>({
  repos: [],
  activeId: 0,
  setSelectedId: () => {},
  isLoading: true,
});

export function ActiveRepoProvider({ children }: { children: ReactNode }) {
  const { data: repos, isLoading } = useRepos();
  const [selectedId, setSelectedIdState] = useState<number>(() => {
    if (typeof window === "undefined") return 0;
    const stored = localStorage.getItem("argus_active_repo");
    return stored ? Number(stored) : 0;
  });

  const setSelectedId = (id: number) => {
    setSelectedIdState(id);
    localStorage.setItem("argus_active_repo", String(id));
  };

  const effectiveId = useMemo(() => {
    const repoList = repos ?? [];
    // User explicitly selected "All repos" (value=0)
    if (selectedId === 0 && typeof window !== "undefined" && localStorage.getItem("argus_active_repo") === "0") return 0;
    // Selected repo exists in list
    if (selectedId && repoList.some(r => r.id === selectedId)) return selectedId;
    // Default to first enabled repo
    const firstEnabled = repoList.find(r => r.enabled);
    return firstEnabled?.id ?? 0;
  }, [selectedId, repos]);

  return (
    <ActiveRepoContext.Provider
      value={{ repos: repos ?? [], activeId: effectiveId, setSelectedId, isLoading }}
    >
      {children}
    </ActiveRepoContext.Provider>
  );
}

export function useActiveRepo() {
  return useContext(ActiveRepoContext);
}
