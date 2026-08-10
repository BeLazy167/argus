"use client";
import { createContext } from "react";
import type { Installation } from "@/lib/types";

export type InstallationContextType = {
	installations: Installation[];
	active: Installation | null;
	setActive: (id: number) => void;
	isLoading: boolean;
};

export const InstallationContext = createContext<InstallationContextType>({
	installations: [],
	active: null,
	setActive: () => {},
	isLoading: true,
});
