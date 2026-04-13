"use client";

import { useState } from "react";
import { useOrganization, useUser } from "@clerk/nextjs";
// Clerk role type — use string for compatibility across Clerk versions
type OrgRole = string;
import { Users, Mail, Loader2, Shield, UserMinus, ChevronDown } from "lucide-react";
import { formatDistanceToNow } from "@/lib/time";

const MEMBER_PARAMS = {
  memberships: { pageSize: 20, keepPreviousData: true },
};

const INVITE_PARAMS = {
  invitations: { pageSize: 20, keepPreviousData: true },
};

const ROLE_OPTIONS: { value: string; label: string }[] = [
  { value: "org:admin", label: "Admin" },
  { value: "org:member", label: "Member" },
];

function roleBadge(role: string) {
  if (role === "org:admin")
    return "border-amber/30 bg-amber/10 text-amber";
  return "border-iron bg-charcoal text-slate-text";
}

export default function TeamPage() {
  const { user } = useUser();
  const { isLoaded, organization, memberships, invitations } = useOrganization({
    ...MEMBER_PARAMS,
    ...INVITE_PARAMS,
  });

  const [email, setEmail] = useState("");
  const [role, setRole] = useState<string>("org:member");
  const [inviting, setInviting] = useState(false);
  const [inviteError, setInviteError] = useState("");

  if (!isLoaded) {
    return (
      <div className="flex items-center justify-center py-20">
        <Loader2 className="h-5 w-5 animate-spin text-slate-text" />
      </div>
    );
  }

  if (!organization) {
    return (
      <div className="py-20 text-center">
        <Users className="h-8 w-8 text-slate-text mx-auto mb-3" />
        <p className="text-sm font-mono text-slate-text">
          Create or select an organization to manage your team.
        </p>
      </div>
    );
  }

  const handleInvite = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.trim()) return;
    setInviting(true);
    setInviteError("");
    try {
      await organization.inviteMember({
        emailAddress: email.trim(),
        role: role as OrgRole,
      });
      await invitations?.revalidate?.();
      setEmail("");
    } catch (err: unknown) {
      setInviteError(err instanceof Error ? err.message : "Failed to send invite");
    } finally {
      setInviting(false);
    }
  };

  return (
    <>
      <div className="mb-8">
        <h1 className="font-mono text-2xl font-bold text-foreground">Team</h1>
        <p className="text-xs font-mono text-slate-text mt-1">
          Manage members, roles, and invitations for{" "}
          <span className="text-amber">{organization.name}</span>.
        </p>
      </div>

      {/* Invite form */}
      <div className="border border-iron bg-charcoal p-5 mb-6">
        <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground mb-4">
          <Mail className="h-3.5 w-3.5 inline-block mr-2 -mt-0.5" />
          Invite member
        </h2>
        <form onSubmit={handleInvite} className="flex items-end gap-3 flex-wrap">
          <div className="flex-1 min-w-[200px]">
            <label className="block text-[10px] font-mono text-slate-text uppercase tracking-wider mb-1">
              Email
            </label>
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="colleague@company.com"
              className="w-full px-3 py-2 text-xs font-mono bg-background border border-iron text-foreground placeholder:text-slate-text/50 focus:border-amber/50 focus:outline-none transition-colors"
              required
            />
          </div>
          <div className="w-36">
            <label className="block text-[10px] font-mono text-slate-text uppercase tracking-wider mb-1">
              Role
            </label>
            <div className="relative">
              <select
                value={role}
                onChange={(e) => setRole(e.target.value)}
                className="w-full appearance-none px-3 py-2 text-xs font-mono bg-background border border-iron text-foreground focus:border-amber/50 focus:outline-none transition-colors pr-8"
              >
                {ROLE_OPTIONS.map((r) => (
                  <option key={r.value} value={r.value}>{r.label}</option>
                ))}
              </select>
              <ChevronDown className="absolute right-2 top-1/2 -translate-y-1/2 h-3 w-3 text-slate-text pointer-events-none" />
            </div>
          </div>
          <button
            type="submit"
            disabled={inviting || !email.trim()}
            className="px-4 py-2 text-xs font-mono bg-amber text-background font-medium hover:bg-amber/90 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {inviting ? <Loader2 className="h-3 w-3 animate-spin" /> : "Send invite"}
          </button>
        </form>
        {inviteError && (
          <p className="text-[10px] font-mono text-red-400 mt-2">{inviteError}</p>
        )}
      </div>

      {/* Members table */}
      <div className="border border-iron bg-charcoal overflow-x-auto mb-6">
        <div className="flex items-center justify-between border-b border-iron px-5 py-4">
          <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
            <Users className="h-3.5 w-3.5 inline-block mr-2 -mt-0.5" />
            Members
          </h2>
          <span className="text-[10px] font-mono text-slate-text">
            {memberships?.data?.length ?? 0} total
          </span>
        </div>

        <table className="w-full min-w-[500px]">
          <thead>
            <tr className="border-b border-iron/50 text-[10px] font-mono uppercase tracking-wider text-slate-text">
              <th className="text-left px-5 py-2.5 font-medium">Member</th>
              <th className="text-left px-3 py-2.5 font-medium">Role</th>
              <th className="text-left px-3 py-2.5 font-medium">Joined</th>
              <th className="text-right px-5 py-2.5 font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {memberships?.data?.map((mem) => {
              const isCurrentUser = mem.publicUserData?.userId === user?.id;
              return (
                <tr key={mem.id} className="border-b border-iron/30 last:border-0">
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-3">
                      {mem.publicUserData?.imageUrl ? (
                        <img
                          src={mem.publicUserData?.imageUrl}
                          alt=""
                          className="h-7 w-7 rounded-full shrink-0"
                        />
                      ) : (
                        <div className="h-7 w-7 rounded-full bg-charcoal border border-iron flex items-center justify-center shrink-0">
                          <span className="text-[10px] font-mono text-slate-text">
                            {(mem.publicUserData?.identifier?.[0] ?? "?").toUpperCase()}
                          </span>
                        </div>
                      )}
                      <div>
                        <p className="text-xs font-mono text-foreground">
                          {mem.publicUserData?.firstName} {mem.publicUserData?.lastName}
                          {isCurrentUser && (
                            <span className="text-[9px] text-slate-text ml-1.5">(you)</span>
                          )}
                        </p>
                        <p className="text-[10px] font-mono text-slate-text">
                          {mem.publicUserData?.identifier}
                        </p>
                      </div>
                    </div>
                  </td>
                  <td className="px-3 py-3">
                    {isCurrentUser ? (
                      <span className={`inline-block border px-2 py-0.5 text-[10px] font-mono ${roleBadge(mem.role)}`}>
                        {mem.role === "org:admin" ? "Admin" : "Member"}
                      </span>
                    ) : (
                      <select
                        value={mem.role}
                        onChange={async (e) => {
                          await mem.update({ role: e.target.value as OrgRole });
                          await memberships?.revalidate();
                        }}
                        className="appearance-none border px-2 py-0.5 text-[10px] font-mono bg-transparent focus:outline-none cursor-pointer border-iron text-foreground"
                      >
                        {ROLE_OPTIONS.map((r) => (
                          <option key={r.value} value={r.value}>{r.label}</option>
                        ))}
                      </select>
                    )}
                  </td>
                  <td className="px-3 py-3">
                    <span className="text-[10px] font-mono text-slate-text">
                      {formatDistanceToNow(mem.createdAt.toISOString())}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-right">
                    {!isCurrentUser && (
                      <button
                        onClick={async () => {
                          await mem.destroy();
                          await memberships?.revalidate();
                        }}
                        className="text-[10px] font-mono text-red-400 hover:text-red-300 transition-colors"
                        title="Remove member"
                      >
                        <UserMinus className="h-3.5 w-3.5" />
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>

        {memberships?.data && (memberships.hasPreviousPage || memberships.hasNextPage) && (
          <div className="flex items-center justify-between px-5 py-3 border-t border-iron/50">
            <button
              disabled={!memberships.hasPreviousPage || memberships.isFetching}
              onClick={() => memberships.fetchPrevious?.()}
              className="text-[10px] font-mono text-slate-text hover:text-foreground disabled:opacity-30 transition-colors"
            >
              ← Prev
            </button>
            <button
              disabled={!memberships.hasNextPage || memberships.isFetching}
              onClick={() => memberships.fetchNext?.()}
              className="text-[10px] font-mono text-slate-text hover:text-foreground disabled:opacity-30 transition-colors"
            >
              Next →
            </button>
          </div>
        )}
      </div>

      {/* Pending invitations */}
      {invitations?.data && invitations.data.length > 0 && (
        <div className="border border-iron bg-charcoal overflow-x-auto">
          <div className="flex items-center justify-between border-b border-iron px-5 py-4">
            <h2 className="text-xs font-mono uppercase tracking-[0.1em] text-foreground">
              <Shield className="h-3.5 w-3.5 inline-block mr-2 -mt-0.5" />
              Pending Invitations
            </h2>
            <span className="text-[10px] font-mono text-slate-text">
              {invitations.data.length} pending
            </span>
          </div>

          <table className="w-full min-w-[400px]">
            <thead>
              <tr className="border-b border-iron/50 text-[10px] font-mono uppercase tracking-wider text-slate-text">
                <th className="text-left px-5 py-2.5 font-medium">Email</th>
                <th className="text-left px-3 py-2.5 font-medium">Role</th>
                <th className="text-left px-3 py-2.5 font-medium">Sent</th>
                <th className="text-right px-5 py-2.5 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {invitations.data.map((inv) => (
                <tr key={inv.id} className="border-b border-iron/30 last:border-0">
                  <td className="px-5 py-3">
                    <span className="text-xs font-mono text-foreground">{inv.emailAddress}</span>
                  </td>
                  <td className="px-3 py-3">
                    <span className={`inline-block border px-2 py-0.5 text-[10px] font-mono ${roleBadge(inv.role)}`}>
                      {inv.role === "org:admin" ? "Admin" : "Member"}
                    </span>
                  </td>
                  <td className="px-3 py-3">
                    <span className="text-[10px] font-mono text-slate-text">
                      {formatDistanceToNow(inv.createdAt.toISOString())}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-right">
                    <button
                      onClick={async () => {
                        await inv.revoke();
                        await invitations?.revalidate?.();
                      }}
                      className="text-[10px] font-mono text-red-400 hover:text-red-300 transition-colors"
                    >
                      Revoke
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
