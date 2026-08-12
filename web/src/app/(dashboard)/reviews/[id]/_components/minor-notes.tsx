import type { ReviewMinorNote } from "@/lib/types";

const severityStyles: Record<string, string> = {
  critical: "border-red-400/30 bg-red-400/10 text-red-400",
  warning: "border-amber/30 bg-amber/10 text-amber",
  suggestion: "border-blue-400/30 bg-blue-400/10 text-blue-400",
  praise: "border-green-400/30 bg-green-400/10 text-green-400",
};

type MinorNotesProps = {
  notes: readonly ReviewMinorNote[];
};

function minorNoteLocation(note: ReviewMinorNote): string {
  if (note.file_path) return `${note.file_path}${note.line > 0 ? `:${note.line}` : ""}`;
  if (note.line > 0) return `Line ${note.line}`;
  return "";
}

export function MinorNotes({ notes }: MinorNotesProps) {
  if (notes.length === 0) return null;

  return (
    <section className="mb-8 border border-iron bg-charcoal/80 p-5" aria-labelledby="minor-notes-heading">
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 id="minor-notes-heading" className="font-mono text-sm font-bold text-foreground">
          Minor Notes
        </h2>
        <span className="font-mono text-[11px] text-slate-text">
          {notes.length} note{notes.length === 1 ? "" : "s"}
        </span>
      </div>
      <ul className="space-y-3">
        {notes.map((note) => {
          const location = minorNoteLocation(note);
          return (
            <li key={note.id} className="border border-iron/60 bg-void/30 px-4 py-3">
              <div className="mb-1.5 flex flex-wrap items-center gap-2">
                {location && <span className="break-all font-mono text-[11px] text-amber">{location}</span>}
                {note.severity && (
                  <span className={`rounded-sm border px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider ${severityStyles[note.severity] ?? "border-iron text-slate-text"}`}>
                    {note.severity}
                  </span>
                )}
              </div>
              {note.title && <p className="whitespace-pre-wrap break-words text-sm text-foreground/80">{note.title}</p>}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
