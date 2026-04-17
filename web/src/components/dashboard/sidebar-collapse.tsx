"use client";

import { useCallback, useSyncExternalStore } from "react";

const KEY = "argus-sidebar-collapsed";
const CHANGE_EVENT = "argus:sidebar-collapsed-change";

/**
 * Cross-tab sidebar collapsed state backed by localStorage + a DOM custom event. Using the
 * custom event (in addition to the native `storage` event, which only fires cross-tab) lets
 * us notify same-tab listeners synchronously when `setCollapsed` runs.
 */
function subscribe(cb: () => void) {
  window.addEventListener("storage", cb);
  window.addEventListener(CHANGE_EVENT, cb);
  return () => {
    window.removeEventListener("storage", cb);
    window.removeEventListener(CHANGE_EVENT, cb);
  };
}

const read = () => localStorage.getItem(KEY) === "1";
const readSSR = () => false;

export function useSidebarCollapsed() {
  const collapsed = useSyncExternalStore(subscribe, read, readSSR);
  const setCollapsed = useCallback((next: boolean) => {
    localStorage.setItem(KEY, next ? "1" : "0");
    window.dispatchEvent(new Event(CHANGE_EVENT));
  }, []);
  return [collapsed, setCollapsed] as const;
}
