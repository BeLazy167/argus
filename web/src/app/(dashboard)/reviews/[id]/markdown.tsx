"use client";

import { useMemo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeRaw from "rehype-raw";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { vscDarkPlus } from "react-syntax-highlighter/dist/esm/styles/prism";

/**
 * Sanitize schema extended to allow the HTML tags the backend's judge prompts
 * generate for collapsible "reason" sections and subtle hint lines:
 * <details>, <summary>, <sub>, plus GFM additions.
 * Everything else falls back to react-markdown's default safe schema.
 */
const sanitizeSchema = {
  ...defaultSchema,
  tagNames: [
    ...(defaultSchema.tagNames ?? []),
    "details",
    "summary",
    "sub",
    "sup",
    "kbd",
    "mark",
  ],
  attributes: {
    ...defaultSchema.attributes,
    // Allow className on code blocks for syntax highlighting
    code: [...(defaultSchema.attributes?.code ?? []), "className"],
    details: ["open"],
  },
};

/** Guess language from file extension for syntax highlighting. */
function langFromPath(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const map: Record<string, string> = {
    ts: "typescript",
    tsx: "tsx",
    js: "javascript",
    jsx: "jsx",
    go: "go",
    py: "python",
    rs: "rust",
    rb: "ruby",
    java: "java",
    kt: "kotlin",
    cs: "csharp",
    css: "css",
    scss: "scss",
    html: "html",
    json: "json",
    yaml: "yaml",
    yml: "yaml",
    toml: "toml",
    sql: "sql",
    sh: "bash",
    bash: "bash",
    md: "markdown",
    dockerfile: "docker",
  };
  return map[ext] ?? "text";
}

const codeBlockStyle = {
  margin: "12px 0",
  borderRadius: "6px",
  fontSize: "12px",
  border: "1px solid oklch(0.18 0 0 / 0.6)",
  background: "oklch(0.07 0 0 / 0.8)",
};

const snippetStyle = {
  margin: 0,
  borderRadius: 0,
  fontSize: "11px",
  lineHeight: "1.65",
  background: "oklch(0.07 0 0 / 0.8)",
  padding: "8px 0",
};

/** Prose markdown with Geist Sans for body, syntax highlighting for code blocks. */
export function Markdown({
  children,
  filePath,
}: {
  children: string;
  filePath?: string;
}) {
  const lang = filePath ? langFromPath(filePath) : "text";

  const components = useMemo(
    () => ({
      h1: ({ children }: { children?: React.ReactNode }) => (
        <h3 className="font-mono text-base font-bold text-foreground mt-5 mb-2 first:mt-0">
          {children}
        </h3>
      ),
      h2: ({ children }: { children?: React.ReactNode }) => (
        <h3 className="font-mono text-base font-bold text-foreground mt-5 mb-2 first:mt-0">
          {children}
        </h3>
      ),
      h3: ({ children }: { children?: React.ReactNode }) => (
        <h4 className="font-mono text-sm font-semibold text-foreground mt-4 mb-1.5 first:mt-0">
          {children}
        </h4>
      ),
      p: ({ children }: { children?: React.ReactNode }) => (
        <p className="text-[13px] text-foreground/80 leading-[1.75] mb-3 last:mb-0">
          {children}
        </p>
      ),
      ul: ({ children }: { children?: React.ReactNode }) => (
        <ul className="list-disc list-outside ml-4 space-y-1.5 mb-3 text-[13px] text-foreground/80">
          {children}
        </ul>
      ),
      ol: ({ children }: { children?: React.ReactNode }) => (
        <ol className="list-decimal list-outside ml-4 space-y-1.5 mb-3 text-[13px] text-foreground/80">
          {children}
        </ol>
      ),
      li: ({ children }: { children?: React.ReactNode }) => (
        <li className="leading-[1.75] pl-1">{children}</li>
      ),
      strong: ({ children }: { children?: React.ReactNode }) => (
        <strong className="font-semibold text-foreground">{children}</strong>
      ),
      code: ({ className, children }: { className?: string; children?: React.ReactNode }) => {
        const match = className?.match(/language-(\w+)/);
        const codeLang = match?.[1] ?? lang;
        const codeStr = String(children).replace(/\n$/, "");

        if (className?.includes("language-") || codeStr.includes("\n")) {
          return (
            <SyntaxHighlighter
              style={vscDarkPlus}
              language={codeLang}
              customStyle={codeBlockStyle}
            >
              {codeStr}
            </SyntaxHighlighter>
          );
        }
        return (
          <code className="bg-amber/10 border border-amber/20 rounded px-1.5 py-0.5 text-[11px] font-mono text-amber">
            {children}
          </code>
        );
      },
      pre: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
      a: ({ href, children }: { href?: string; children?: React.ReactNode }) => (
        <a
          href={href}
          className="text-amber hover:underline underline-offset-2"
          target="_blank"
          rel="noopener noreferrer"
        >
          {children}
        </a>
      ),
      details: ({ children }: { children?: React.ReactNode }) => (
        <details className="my-2 rounded border border-iron/40 bg-charcoal/40 px-3 py-2 text-[13px] text-foreground/80">
          {children}
        </details>
      ),
      summary: ({ children }: { children?: React.ReactNode }) => (
        <summary className="cursor-pointer font-mono text-[12px] text-foreground/90 hover:text-foreground transition-colors [&::-webkit-details-marker]:text-amber/60">
          {children}
        </summary>
      ),
      sub: ({ children }: { children?: React.ReactNode }) => (
        <sub className="text-[10px] font-mono text-slate-text/60 block mt-2 not-italic">
          {children}
        </sub>
      ),
      table: ({ children }: { children?: React.ReactNode }) => (
        <div className="overflow-x-auto my-3">
          <table className="min-w-full text-[12px] border-collapse border border-iron/40">
            {children}
          </table>
        </div>
      ),
      th: ({ children }: { children?: React.ReactNode }) => (
        <th className="border border-iron/40 px-2 py-1 font-mono text-left bg-charcoal/60">
          {children}
        </th>
      ),
      td: ({ children }: { children?: React.ReactNode }) => (
        <td className="border border-iron/40 px-2 py-1 align-top">{children}</td>
      ),
    }),
    [lang],
  );

  return (
    <div className="font-sans">
      <ReactMarkdown
        components={components}
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeRaw, [rehypeSanitize, sanitizeSchema]]}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}

export function CodeSnippet({
  code,
  startLine,
  language,
}: {
  code: string;
  startLine?: number;
  language: string;
}) {
  if (!code) return null;
  const start = startLine ?? 1;

  return (
    <div className="mx-4 mt-3 mb-1 rounded-md overflow-hidden border border-iron/40">
      <SyntaxHighlighter
        style={vscDarkPlus}
        language={language}
        showLineNumbers
        startingLineNumber={start}
        lineNumberStyle={{
          minWidth: "3em",
          paddingRight: "1em",
          color: "oklch(0.18 0 0 / 0.6)",
          borderRight: "1px solid oklch(0.18 0 0 / 0.3)",
          marginRight: "1em",
          userSelect: "none",
        }}
        customStyle={snippetStyle}
        wrapLines
        lineProps={() => ({
          style: { display: "flex", paddingRight: "1em" },
          className: "hover:bg-[oklch(0.18_0_0/0.15)]",
        })}
      >
        {code}
      </SyntaxHighlighter>
    </div>
  );
}
