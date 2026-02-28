"use client";

import { useState } from "react";
import { ScrollText, Plus, Trash2, Loader2, ToggleLeft, ToggleRight } from "lucide-react";
import {
  useRules,
  useCreateRule,
  useUpdateRule,
  useDeleteRule,
} from "@/lib/queries/rules";

const CATEGORIES = [
  "security", "performance", "style", "testing",
  "documentation", "error-handling", "accessibility", "other",
] as const;

const PRIORITIES = [
  { value: 0, label: "Low" },
  { value: 1, label: "Medium" },
  { value: 2, label: "High" },
] as const;

const categoryColors: Record<string, string> = {
  security: "bg-red-400/10 text-red-400 border-red-400/20",
  performance: "bg-amber/10 text-amber border-amber/20",
  style: "bg-blue-400/10 text-blue-400 border-blue-400/20",
  testing: "bg-green-400/10 text-green-400 border-green-400/20",
  documentation: "bg-purple-400/10 text-purple-400 border-purple-400/20",
  "error-handling": "bg-orange-400/10 text-orange-400 border-orange-400/20",
  accessibility: "bg-cyan-400/10 text-cyan-400 border-cyan-400/20",
  other: "bg-iron/50 text-slate-text border-iron",
};

const priorityLabel = (p: number) =>
  PRIORITIES.find((pr) => pr.value === p)?.label ?? "Low";

export default function RulesPage() {
  const { data: rules, isLoading } = useRules();
  const createRule = useCreateRule();
  const updateRule = useUpdateRule();
  const deleteRule = useDeleteRule();

  const [showForm, setShowForm] = useState(false);
  const [category, setCategory] = useState<string>(CATEGORIES[0]);
  const [content, setContent] = useState("");
  const [priority, setPriority] = useState(0);

  const [editingId, setEditingId] = useState<number | null>(null);
  const [editCategory, setEditCategory] = useState("");
  const [editContent, setEditContent] = useState("");
  const [editPriority, setEditPriority] = useState(0);

  const handleCreate = () => {
    if (!category || !content) return;
    createRule.mutate(
      { category, content, priority, enabled: true },
      {
        onSuccess: () => {
          setCategory(CATEGORIES[0]);
          setContent("");
          setPriority(0);
          setShowForm(false);
        },
      },
    );
  };

  const startEdit = (rule: { id: number; category: string; content: string; priority: number }) => {
    setEditingId(rule.id);
    setEditCategory(rule.category);
    setEditContent(rule.content);
    setEditPriority(rule.priority);
  };

  const handleUpdate = (ruleId: number, enabled: boolean) => {
    updateRule.mutate(
      { id: ruleId, category: editCategory, content: editContent, priority: editPriority, enabled },
      { onSuccess: () => setEditingId(null) },
    );
  };

  return (
    <>
      <div className="mb-8 flex items-center justify-between">
        <div>
          <h1 className="font-display text-2xl font-bold text-foreground">
            Rules
          </h1>
          <p className="text-xs font-mono text-slate-text mt-1">
            Rules tell Argus what to watch for in every PR review.
          </p>
        </div>
        <button
          type="button"
          onClick={() => setShowForm(!showForm)}
          className="flex items-center gap-2 rounded-md border border-amber/30 bg-amber/10 px-3 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors"
        >
          <Plus className="h-3.5 w-3.5" />
          Add rule
        </button>
      </div>

      {/* Create form */}
      {showForm && (
        <div className="mb-6 rounded-lg border border-amber/30 bg-charcoal p-5 space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
                Category
              </label>
              <select
                value={category}
                onChange={(e) => setCategory(e.target.value)}
                className="w-full rounded-md border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
              >
                {CATEGORIES.map((c) => (
                  <option key={c} value={c}>{c}</option>
                ))}
              </select>
            </div>
            <div>
              <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
                Priority
              </label>
              <div className="flex">
                {PRIORITIES.map((p) => (
                  <button
                    key={p.value}
                    type="button"
                    onClick={() => setPriority(p.value)}
                    className={`flex-1 border px-2 py-2 text-xs font-mono transition-colors first:rounded-l-md last:rounded-r-md ${
                      priority === p.value
                        ? "bg-amber/10 text-amber border-amber/30"
                        : "bg-background text-slate-text border-iron"
                    }`}
                  >
                    {p.label}
                  </button>
                ))}
              </div>
            </div>
          </div>
          <div>
            <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
              Rule content
            </label>
            <textarea
              value={content}
              onChange={(e) => setContent(e.target.value)}
              rows={3}
              placeholder="Describe what Argus should check for..."
              className="w-full rounded-md border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-iron focus:border-amber focus:outline-none resize-none"
            />
          </div>
          <div className="flex justify-end gap-3">
            <button
              type="button"
              onClick={() => setShowForm(false)}
              className="rounded-md px-3 py-1.5 text-xs font-mono text-slate-text hover:text-foreground transition-colors"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleCreate}
              disabled={createRule.isPending || !category || !content}
              className="rounded-md border border-amber bg-amber/10 px-4 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
            >
              {createRule.isPending ? "Creating..." : "Create rule"}
            </button>
          </div>
        </div>
      )}

      {/* Rules list */}
      {isLoading ? (
        <div className="flex items-center justify-center py-20">
          <Loader2 className="h-6 w-6 animate-spin text-slate-text" />
        </div>
      ) : rules?.length === 0 ? (
        <div className="rounded-lg border border-iron bg-charcoal p-10 text-center">
          <ScrollText className="h-8 w-8 text-slate-text mx-auto mb-3" />
          <p className="text-sm font-mono text-foreground mb-1">No rules yet</p>
          <p className="text-xs font-mono text-slate-text">
            Add rules to guide how Argus reviews code.
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {rules?.map((rule) => (
            <div
              key={rule.id}
              className={`rounded-lg border border-iron bg-charcoal px-5 py-4 cursor-pointer ${
                !rule.enabled ? "opacity-50" : ""
              }`}
              onClick={(e) => {
                const target = e.target as HTMLElement;
                if (target.closest("button")) return;
                if (editingId === rule.id) return;
                startEdit(rule);
              }}
            >
              <div className="flex items-start justify-between mb-2">
                <div className="flex items-center gap-3">
                  <span
                    className={`inline-flex items-center rounded-sm border px-2 py-0.5 text-[10px] font-mono uppercase tracking-wider ${
                      categoryColors[rule.category] ?? categoryColors.other
                    }`}
                  >
                    {rule.category}
                  </span>
                  <span className="text-[10px] font-mono text-iron">
                    {priorityLabel(rule.priority)}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() =>
                      updateRule.mutate({
                        id: rule.id,
                        enabled: !rule.enabled,
                      })
                    }
                    className="text-slate-text hover:text-foreground transition-colors"
                  >
                    {rule.enabled ? (
                      <ToggleRight className="h-4 w-4 text-green-400" />
                    ) : (
                      <ToggleLeft className="h-4 w-4" />
                    )}
                  </button>
                  <button
                    type="button"
                    onClick={() => deleteRule.mutate(rule.id)}
                    className="text-slate-text hover:text-red-400 transition-colors"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              </div>

              {editingId === rule.id ? (
                <div className="mt-3 space-y-4 border-t border-iron pt-4">
                  <div className="grid grid-cols-2 gap-4">
                    <div>
                      <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
                        Category
                      </label>
                      <select
                        value={editCategory}
                        onChange={(e) => setEditCategory(e.target.value)}
                        className="w-full rounded-md border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground focus:border-amber focus:outline-none"
                      >
                        {CATEGORIES.map((c) => (
                          <option key={c} value={c}>{c}</option>
                        ))}
                      </select>
                    </div>
                    <div>
                      <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
                        Priority
                      </label>
                      <div className="flex">
                        {PRIORITIES.map((p) => (
                          <button
                            key={p.value}
                            type="button"
                            onClick={() => setEditPriority(p.value)}
                            className={`flex-1 border px-2 py-2 text-xs font-mono transition-colors first:rounded-l-md last:rounded-r-md ${
                              editPriority === p.value
                                ? "bg-amber/10 text-amber border-amber/30"
                                : "bg-background text-slate-text border-iron"
                            }`}
                          >
                            {p.label}
                          </button>
                        ))}
                      </div>
                    </div>
                  </div>
                  <div>
                    <label className="block text-[11px] font-mono uppercase tracking-wider text-slate-text mb-1">
                      Rule content
                    </label>
                    <textarea
                      value={editContent}
                      onChange={(e) => setEditContent(e.target.value)}
                      rows={3}
                      className="w-full rounded-md border border-iron bg-background px-3 py-2 text-xs font-mono text-foreground focus:border-amber focus:outline-none resize-none"
                    />
                  </div>
                  <div className="flex justify-end gap-3">
                    <button
                      type="button"
                      onClick={() => setEditingId(null)}
                      className="rounded-md px-3 py-1.5 text-xs font-mono text-slate-text hover:text-foreground transition-colors"
                    >
                      Cancel
                    </button>
                    <button
                      type="button"
                      onClick={() => handleUpdate(rule.id, rule.enabled)}
                      disabled={updateRule.isPending || !editCategory || !editContent}
                      className="rounded-md border border-amber bg-amber/10 px-4 py-1.5 text-xs font-mono text-amber hover:bg-amber/20 transition-colors disabled:opacity-50"
                    >
                      {updateRule.isPending ? "Saving..." : "Save"}
                    </button>
                  </div>
                </div>
              ) : (
                <p className="text-xs font-mono text-foreground/80 whitespace-pre-wrap">
                  {rule.content}
                </p>
              )}
            </div>
          ))}
        </div>
      )}
    </>
  );
}
