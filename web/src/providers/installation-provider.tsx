"use client";
import { createContext, useContext, useState, useEffect, type ReactNode } from "react";
import { useMyInstallations } from "@/lib/queries/installations";
import type { Installation } from "@/lib/types";

type InstallationContextType = {
  installations: Installation[];
  active: Installation | null;
  setActive: (id: number) => void;
  isLoading: boolean;
};

const InstallationContext = createContext<InstallationContextType>({
  installations: [],
  active: null,
  setActive: () => {},
  isLoading: true,
});

export function InstallationProvider({ children }: { children: ReactNode }) {
  const { data: installations, isLoading } = useMyInstallations();
  const [activeId, setActiveId] = useState<number | null>(null);

  useEffect(() => {
    const first = installations?.[0];
    if (!first || activeId) return;
    const stored = localStorage.getItem("argus_active_installation");
    const id = stored ? Number(stored) : first.id;
    setActiveId(id);
  }, [installations, activeId]);

  const active = installations?.find((i) => i.id === activeId) ?? null;
  const setActive = (id: number) => {
    setActiveId(id);
    localStorage.setItem("argus_active_installation", String(id));
  };

  return (
    <InstallationContext.Provider value={{ installations: installations ?? [], active, setActive, isLoading }}>
      {children}
    </InstallationContext.Provider>
  );
}

export const useInstallation = () => useContext(InstallationContext);
