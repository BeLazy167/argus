import { useState } from "react";
import { useRepos } from "@/lib/queries/repos";

/** Shared repo selection state used across dashboard pages. */
export function useActiveRepo() {
  const { data: repos, isLoading } = useRepos();
  const [selectedId, setSelectedId] = useState(0);

  const activeId = selectedId;

  return { repos: repos ?? [], activeId, setSelectedId, isLoading };
}
