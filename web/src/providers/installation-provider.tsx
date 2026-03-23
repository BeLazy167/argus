"use client";
import { createContext, useContext, type ReactNode } from "react";
import { useOrganization, useAuth } from "@clerk/nextjs";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
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
  const { getToken } = useAuth();
  const { organization } = useOrganization();

  const orgId = organization?.id ?? "personal";

  const { data: installations, isLoading: listLoading } = useQuery({
    queryKey: ["my-installations", orgId],
    queryFn: async () => {
      const token = await getToken({ skipCache: true });
      return api.get<Installation[]>("/api/v1/me/installations", token ?? undefined);
    },
  });

  const { data: current, isLoading: currentLoading } = useQuery({
    queryKey: ["current-installation", orgId],
    queryFn: async () => {
      const token = await getToken({ skipCache: true });
      return api.get<Installation>("/api/v1/installations/current", token ?? undefined);
    },
  });

  const isLoading = listLoading || currentLoading;

  return (
    <InstallationContext.Provider
      value={{
        installations: installations ?? [],
        active: current ?? null,
        setActive: () => {},
        isLoading,
      }}
    >
      {children}
    </InstallationContext.Provider>
  );
}

export const useInstallation = () => useContext(InstallationContext);
