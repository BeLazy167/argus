"use client";
import { createContext, useContext, useState, type ReactNode } from "react";
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
    if (id) {
      localStorage.setItem("argus_active_repo", String(id));
    } else {
      localStorage.removeItem("argus_active_repo");
    }
  };

  return (
    <ActiveRepoContext.Provider
      value={{ repos: repos ?? [], activeId: selectedId, setSelectedId, isLoading }}
    >
      {children}
    </ActiveRepoContext.Provider>
  );
}

export function useActiveRepo() {
  return useContext(ActiveRepoContext);
}
