"use client";
import { createContext, useContext, useEffect, useRef, type ReactNode } from "react";
import { useOrganization, useAuth } from "@clerk/nextjs";
import { useQuery, useQueryClient } from "@tanstack/react-query";
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
  const qc = useQueryClient();
  const autoLinked = useRef<string | null>(null);

  const orgId = organization?.id ?? "personal";
  const orgSlug = organization?.slug ?? organization?.name ?? "";

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

  // Auto-link: if in a Clerk org but no installation scoped, try to match by org name
  useEffect(() => {
    if (!organization || !orgSlug || !installations?.length) return;
    if (current) return; // already scoped — no need to auto-link
    if (autoLinked.current === orgId) return; // already tried

    const match = installations.find(
      (i) => i.org_login.toLowerCase() === orgSlug.toLowerCase() && !i.clerk_org_id,
    );
    if (!match) return;

    autoLinked.current = orgId;
    getToken({ skipCache: true }).then((token) => {
      api
        .post("/api/v1/installations/auto-link", { org_slug: orgSlug }, token ?? undefined)
        .then(() => {
          qc.invalidateQueries({ queryKey: ["current-installation"] });
          qc.invalidateQueries({ queryKey: ["my-installations"] });
        });
    });
  }, [organization, orgId, orgSlug, installations, current, getToken, qc]);

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
