import type { Metadata } from "next";
import { LastUpdated } from "@/components/seo/last-updated";

export const metadata: Metadata = {
  title: "MCP server — Argus docs",
  description:
    "Connect your own agent to Argus over the Model Context Protocol: OAuth login, one organization per connection, nine scope-gated tools over team memory and reviews.",
};

export default function MCPPage() {
  return (
    <article className="space-y-6">
      <h1 className="text-2xl font-mono text-slate-100">MCP server</h1>
      <LastUpdated date="2026-09-14" />

      <p>
        Argus exposes team memory and review results over the Model Context Protocol. Any MCP
        client that supports remote servers with OAuth — Claude Code, Cursor, Claude Desktop —
        can read what Argus learned about your codebase and query past reviews without leaving
        the agent.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Connect</h2>
      <p>
        Point your client at{" "}
        <code className="bg-slate-900 px-1 text-amber">https://api.argus.reviews/mcp</code>. The
        client discovers the authorization server from{" "}
        <code className="bg-slate-900 px-1 text-amber">
          /.well-known/oauth-protected-resource
        </code>
        , runs a browser login once, and asks you to pick{" "}
        <strong className="text-slate-200">one organization</strong> — the connection sees only
        that org&apos;s repos and memory. Switching orgs means authorizing again. Tokens refresh
        automatically; there are no API keys.
      </p>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Tools</h2>
      <p>
        Call <code className="bg-slate-900 px-1 text-amber">list_repos</code> first — it resolves
        the local <code className="bg-slate-900 px-1 text-amber">repo_id</code> and{" "}
        <code className="bg-slate-900 px-1 text-amber">installation_id</code> every other tool
        takes.
      </p>
      <ul className="list-disc pl-5 space-y-1 text-slate-400">
        <li>
          <code className="bg-slate-900 px-1 text-amber">search_memory</code>,{" "}
          <code className="bg-slate-900 px-1 text-amber">get_memory_briefing</code> — semantic
          search over team memory, and the briefing Argus gives its own reviewers.
        </li>
        <li>
          <code className="bg-slate-900 px-1 text-amber">create_memory</code>,{" "}
          <code className="bg-slate-900 px-1 text-amber">delete_memory</code>,{" "}
          <code className="bg-slate-900 px-1 text-amber">retire_memory</code> — write and manage
          memory. Need the <code className="bg-slate-900 px-1 text-amber">argus:memory:write</code>{" "}
          scope.
        </li>
        <li>
          <code className="bg-slate-900 px-1 text-amber">list_reviews</code>,{" "}
          <code className="bg-slate-900 px-1 text-amber">get_review_status</code>,{" "}
          <code className="bg-slate-900 px-1 text-amber">get_review</code> — review history, cheap
          status polls, and the full review page in one call.
        </li>
      </ul>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Scopes and guard rails</h2>
      <p>
        <code className="bg-slate-900 px-1 text-amber">argus:read</code> covers every read tool;{" "}
        <code className="bg-slate-900 px-1 text-amber">argus:memory:write</code> covers the three
        memory mutations. A read-only grant cannot mutate memory, whatever flags a call sets.
      </p>
      <ul className="list-disc pl-5 space-y-1 text-slate-400">
        <li>
          Deleting or retiring a memory the review pipeline learned requires{" "}
          <code className="bg-slate-900 px-1 text-amber">confirm_pipeline_learned=true</code>;
          writing an org-wide memory requires{" "}
          <code className="bg-slate-900 px-1 text-amber">confirm_shared=true</code>. Neither can be
          undone.
        </li>
        <li>
          To stop a memory influencing reviews, use{" "}
          <code className="bg-slate-900 px-1 text-amber">retire_memory</code> —{" "}
          <code className="bg-slate-900 px-1 text-amber">delete_memory</code> removes one
          contributing record and the memory may remain searchable.
        </li>
      </ul>

      <h2 className="text-lg font-mono text-slate-100 pt-4">Self-hosting</h2>
      <p>
        Off by default. Set{" "}
        <code className="bg-slate-900 px-1 text-amber">MCP_ENABLED=true</code>,{" "}
        <code className="bg-slate-900 px-1 text-amber">CLERK_ISSUER_URL</code> (your Clerk Frontend
        API origin), and <code className="bg-slate-900 px-1 text-amber">MCP_RESOURCE_URL</code>{" "}
        (the public <code className="bg-slate-900 px-1 text-amber">https://…/mcp</code> URL);{" "}
        <code className="bg-slate-900 px-1 text-amber">CLERK_JWKS_URL</code> must already be set.
        The route 404s while disabled. Full setup, including the Clerk dashboard configuration,
        is in the{" "}
        <a
          href="https://github.com/BeLazy167/argus#mcp-use-arguss-memory-and-reviews-from-your-own-agent"
          className="text-amber hover:text-slate-100"
        >
          README
        </a>
        .
      </p>
    </article>
  );
}
