"use client";
import { OrganizationProfile } from "@clerk/nextjs";

export default function TeamPage() {
  return (
    <>
      <div className="mb-8">
        <h1 className="font-mono text-2xl font-bold text-foreground">Team</h1>
        <p className="text-xs font-mono text-slate-text mt-1">
          Manage members, roles, and invitations.
        </p>
      </div>
      <OrganizationProfile
        appearance={{
          elements: {
            rootBox: "w-full",
            cardBox: "shadow-none border border-iron bg-charcoal rounded-lg",
            navbar: "hidden",
          }
        }}
      />
    </>
  );
}
