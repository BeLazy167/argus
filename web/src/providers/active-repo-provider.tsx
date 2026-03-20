"use client";
import { createContext, useContext, useState, useEffect, type ReactNode } from "react";
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
  const [selectedId, setSelectedIdState] = useState(0);

  useEffect(() => {
    if (!repos?.length || selectedId) return;
    const stored = localStorage.getItem("argus_active_repo");
    const id = stored ? Number(stored) : 0;
    if (id && repos.some((r) => r.id === id)) {
      setSelectedIdState(id);
    }
  }, [repos, selectedId]);

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
