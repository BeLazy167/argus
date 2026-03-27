import dagre from "dagre";
import { Position, type Node, type Edge } from "@xyflow/react";

const NODE_WIDTH = 180;
const NODE_HEIGHT = 60;
const GROUP_PADDING = 40;

/** Extract directory group from a file path (e.g. "src/lib/core/interfaces.ts" → "src/lib/core") */
function dirGroup(filePath: string): string {
  const parts = filePath.split("/");
  if (parts.length <= 1) return "root";
  return parts.slice(0, -1).join("/");
}

/** Get a short label for a directory group */
function groupLabel(dir: string): string {
  const parts = dir.split("/");
  return parts[parts.length - 1] || dir;
}

export function getLayoutedElements(
  nodes: Node[],
  edges: Edge[],
  direction: "TB" | "LR" = "TB"
) {
  const g = new dagre.graphlib.Graph({ compound: true }).setDefaultEdgeLabel(
    () => ({})
  );
  g.setGraph({ rankdir: direction, nodesep: 50, ranksep: 100, marginx: 30, marginy: 30 });

  // Collect unique groups from file paths
  const groups = new Map<string, string[]>(); // dir → node IDs
  nodes.forEach((node) => {
    const fp = (node.data?.filePath as string) || "";
    const group = dirGroup(fp);
    if (!groups.has(group)) groups.set(group, []);
    groups.get(group)!.push(node.id);
  });

  // Create group (parent) nodes
  const groupNodes: Node[] = [];
  groups.forEach((memberIds, dir) => {
    if (memberIds.length < 2) return; // don't group singletons
    const groupId = `group:${dir}`;
    g.setNode(groupId, {
      width: NODE_WIDTH * 2 + GROUP_PADDING,
      height: NODE_HEIGHT * Math.ceil(memberIds.length / 2) + GROUP_PADDING * 2,
    });
    groupNodes.push({
      id: groupId,
      type: "group",
      position: { x: 0, y: 0 },
      data: { label: groupLabel(dir) },
      style: {
        width: NODE_WIDTH * 2 + GROUP_PADDING * 2,
        height: NODE_HEIGHT * Math.ceil(memberIds.length / 2) + GROUP_PADDING * 3,
        backgroundColor: "rgba(30, 30, 40, 0.6)",
        borderRadius: 12,
        border: "1px solid rgba(100, 116, 139, 0.2)",
        padding: GROUP_PADDING,
      },
    });
  });

  // Add all real nodes with group parents
  nodes.forEach((node) => {
    const fp = (node.data?.filePath as string) || "";
    const group = dirGroup(fp);
    const groupId = `group:${group}`;
    g.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
    if (groups.has(group) && groups.get(group)!.length >= 2) {
      g.setParent(node.id, groupId);
    }
  });

  edges.forEach((edge) => {
    g.setEdge(edge.source, edge.target);
  });

  dagre.layout(g);

  const isHorizontal = direction === "LR";

  // Position group nodes
  const positionedGroups = groupNodes.map((gn) => {
    const pos = g.node(gn.id);
    if (!pos) return gn;
    return {
      ...gn,
      position: { x: pos.x - (pos.width || 0) / 2, y: pos.y - (pos.height || 0) / 2 },
    };
  });

  // Position child nodes (relative to parent if grouped)
  const positionedNodes = nodes.map((node) => {
    const pos = g.node(node.id);
    const fp = (node.data?.filePath as string) || "";
    const group = dirGroup(fp);
    const groupId = `group:${group}`;
    const hasGroup = groups.has(group) && groups.get(group)!.length >= 2;

    let x = pos.x - NODE_WIDTH / 2;
    let y = pos.y - NODE_HEIGHT / 2;

    // If grouped, make position relative to parent
    if (hasGroup) {
      const parentPos = g.node(groupId);
      if (parentPos) {
        x = x - (parentPos.x - (parentPos.width || 0) / 2);
        y = y - (parentPos.y - (parentPos.height || 0) / 2);
      }
    }

    return {
      ...node,
      targetPosition: isHorizontal ? Position.Left : Position.Top,
      sourcePosition: isHorizontal ? Position.Right : Position.Bottom,
      position: { x, y },
      ...(hasGroup ? { parentId: groupId, extent: "parent" as const } : {}),
    };
  });

  return {
    nodes: [...positionedGroups, ...positionedNodes],
    edges,
  };
}
