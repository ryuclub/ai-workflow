import { useMemo } from "react";
import ReactFlow, {
  Background,
  Controls,
  type Edge,
  type Node,
  MarkerType,
  Position,
} from "reactflow";
import "reactflow/dist/style.css";
import type { NodeRun, Pipeline } from "../types";

const STATE_COLOR: Record<string, string> = {
  pending: "#9aa0a6",
  running: "#f5a623",
  ok: "#2ecc71",
  fail: "#e74c3c",
  waiting: "#3498db",
  skip: "#7e8aa0",
};

const STATE_LABEL: Record<string, string> = {
  pending: "待跑",
  running: "运行中",
  ok: "完成",
  fail: "失败",
  waiting: "等人审",
  skip: "已跳过",
};

// 终点节点专属配色（红绿蓝 + 过渡色，命中时渐变填充；与普通步骤的描边风格区分）。
const TERMINAL: Record<string, { color: string; grad: string }> = {
  done: { color: "#2e7d32", grad: "linear-gradient(135deg, #43a047, #7cb342)" }, // 绿
  skipped: { color: "#1565c0", grad: "linear-gradient(135deg, #1e88e5, #42a5f5)" }, // 蓝
  failed: { color: "#c62828", grad: "linear-gradient(135deg, #e53935, #ef5350)" }, // 红
};

// 终点是否被当前任务终态命中。
function terminalActive(nodeId: string, taskState: string): boolean {
  if (nodeId === "done") return taskState === "done";
  if (nodeId === "skipped") return taskState === "skipped";
  if (nodeId === "failed") return ["adjudication", "failed", "rejected", "canceled"].includes(taskState);
  return false;
}

const COLW = 185;
const ROWH = 96;

// 节点朝向字段（后端布局给出）→ ReactFlow Position。空则默认横向流（target 左、source 右）。
const POS: Record<string, Position> = {
  left: Position.Left,
  right: Position.Right,
  top: Position.Top,
  bottom: Position.Bottom,
};
const srcPos = (p?: string) => (p ? POS[p] : Position.Right);
const tgtPos = (p?: string) => (p ? POS[p] : Position.Left);

export default function PipelineView({
  pipeline,
  runs,
  taskState,
}: {
  pipeline: Pipeline;
  runs: NodeRun[];
  taskState: string;
}) {
  const stateById = useMemo(() => {
    const m: Record<string, string> = {};
    for (const r of runs) m[r.node_id] = r.state;
    return m;
  }, [runs]);

  const nodes: Node[] = useMemo(
    () =>
      pipeline.nodes.map((n) => {
        const pos = { x: n.x * COLW, y: n.y * ROWH };

        // 终点：实心色块（命中时填充专属色 + 白字；未命中淡显虚线）。
        if (n.kind === "terminal") {
          const t = TERMINAL[n.id] || { color: "#9aa0a6", grad: "#9aa0a6" };
          const active = terminalActive(n.id, taskState);
          return {
            id: n.id,
            position: pos,
            data: {
              label: (
                <div style={{ textAlign: "center", color: active ? "#fff" : "#9aa0a6" }}>
                  <div style={{ fontSize: 12, fontWeight: 700 }}>◉ {n.name}</div>
                  <div style={{ fontSize: 10 }}>{active ? "← 已抵达" : "终点"}</div>
                </div>
              ),
            },
            sourcePosition: srcPos(n.sourcePos),
            targetPosition: tgtPos(n.targetPos),
            style: {
              border: `2px ${active ? "solid" : "dashed"} ${active ? t.color : "#ccc"}`,
              borderRadius: 20,
              padding: 6,
              width: 150,
              background: active ? t.grad : "#fafafa",
              opacity: active ? 1 : 0.5,
              boxShadow: active ? `0 0 0 4px ${t.color}33` : "none",
            },
          };
        }

        // 普通步骤：描边风格，按运行态着色。
        const st = stateById[n.id] || "pending";
        const color = STATE_COLOR[st] || "#9aa0a6";
        return {
          id: n.id,
          position: pos,
          data: {
            label: (
              <div style={{ textAlign: "center" }}>
                <div style={{ fontSize: 12, fontWeight: 600 }}>{n.name}</div>
                <div style={{ fontSize: 10, color }}>
                  {n.kind === "gate" ? "⏸ " : ""}
                  {STATE_LABEL[st] || st}
                </div>
              </div>
            ),
          },
          sourcePosition: srcPos(n.sourcePos),
          targetPosition: tgtPos(n.targetPos),
          style: {
            border: `2px solid ${color}`,
            borderRadius: 8,
            padding: 6,
            width: 150,
            background: st === "running" ? "#fff8e1" : "#fff",
            boxShadow: st === "running" ? `0 0 0 3px ${color}33` : "none",
          },
        };
      }),
    [pipeline, stateById, taskState],
  );

  const edges: Edge[] = useMemo(
    () =>
      pipeline.edges.map((e) => {
        const toTerminalBranch = e.to === "skipped" || e.to === "failed";
        return {
          id: `${e.from}-${e.to}`,
          source: e.from,
          target: e.to,
          type: "smoothstep", // 直角折线：U 形折角/回边比贝塞尔更清爽
          animated: stateById[e.from] === "running",
          style: toTerminalBranch ? { strokeDasharray: "4 4", stroke: "#bbb" } : undefined,
          markerEnd: { type: MarkerType.ArrowClosed },
        };
      }),
    [pipeline, stateById],
  );

  return (
    <div style={{ height: 420, border: "1px solid #e0e0e0", borderRadius: 8 }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        fitView
        nodesDraggable
        nodesConnectable={false}
        proOptions={{ hideAttribution: true }}
      >
        <Background />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}
