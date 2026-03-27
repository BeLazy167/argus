import dagre from "dagre";
import { Position, type Node, type Edge } from "@xyflow/react";

const NODE_WIDTH = 160;
const NODE_HEIGHT = 44;
const GROUP_PAD_X = 30;
const GROUP_PAD_Y = 50; // extra top for label

/** Extract directory group from file path */
function dirGroup(filePath: string): string {
  const parts = filePath.split("/");
  if (parts.length <= 1) return "root";
  return parts.slice(0, -1).join("/");
}

/** Short readable label for a directory */
function groupLabel(dir: string): string {
  const parts = dir.split("/");
  // Use last 2 segments for readability: "lib/providers" not just "providers"
  return parts.slice(-2).join("/");
}

export function getLayoutedElements(
  nodes: Node[],
  edges: Edge[],
  direction: "TB" | "LR" = "TB"
) {
  // Phase 1: Compute groups
  const groups = new Map<string, string[]>();
  nodes.forEach((node) => {
    const fp = (node.data?.filePath as string) || "";
    const group = dirGroup(fp);
    if (!groups.has(group)) groups.set(group, []);
    groups.get(group)!.push(node.id);
  });

  // Phase 2: Layout with dagre (flat — no compound, more reliable)
  const g = new dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}));
  g.setGraph({
    rankdir: direction,
    nodesep: 70,
    ranksep: 120,
    marginx: 50,
    marginy: 50,
  });

  nodes.forEach((node) => {
    g.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  });
  edges.forEach((edge) => {
    g.setEdge(edge.source, edge.target);
  });

  dagre.layout(g);

  const isHorizontal = direction === "LR";

  // Position nodes from dagre
  const positionedNodes = nodes.map((node) => {
    const pos = g.node(node.id);
    return {
      ...node,
      targetPosition: isHorizontal ? Position.Left : Position.Top,
      sourcePosition: isHorizontal ? Position.Right : Position.Bottom,
      position: { x: pos.x - NODE_WIDTH / 2, y: pos.y - NODE_HEIGHT / 2 },
    };
  });

  // Phase 3: Compute group bounding boxes from positioned nodes
  const groupNodes: Node[] = [];
  groups.forEach((memberIds, dir) => {
    if (memberIds.length < 2) return;

    // Find bounding box of member nodes
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const id of memberIds) {
      const node = positionedNodes.find((n) => n.id === id);
      if (!node) continue;
      minX = Math.min(minX, node.position.x);
      minY = Math.min(minY, node.position.y);
      maxX = Math.max(maxX, node.position.x + NODE_WIDTH);
      maxY = Math.max(maxY, node.position.y + NODE_HEIGHT);
    }

    const groupId = `group:${dir}`;
    const gx = minX - GROUP_PAD_X;
    const gy = minY - GROUP_PAD_Y;
    const gw = maxX - minX + NODE_WIDTH + GROUP_PAD_X * 2;
    const gh = maxY - minY + NODE_HEIGHT + GROUP_PAD_Y + GROUP_PAD_X;

    groupNodes.push({
      id: groupId,
      type: "group",
      position: { x: gx, y: gy },
      data: { label: groupLabel(dir) },
      style: {
        width: gw,
        height: gh,
        backgroundColor: "rgba(20, 20, 30, 0.5)",
        borderRadius: 16,
        border: "1px solid rgba(71, 85, 105, 0.15)",
        pointerEvents: "none" as const,
      },
      selectable: false,
      draggable: false,
    });
  });

  // Groups go first (rendered behind), then nodes on top
  return {
    nodes: [...groupNodes, ...positionedNodes],
    edges,
  };
}
