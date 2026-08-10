"use client";

import { AreaChart, Area, XAxis, YAxis, Tooltip } from "recharts";
import { SectionHeader, Loading, Err } from "./stat-cards";
import { ChartCard } from "./chart-card";
import { fmt$, fmtTok, tooltipStyle } from "./formatters";

interface ChartPoint {
  day: string;
  review_count: number;
  avg_score: number;
  total_cost: number;
  total_tokens: number;
}

interface TrendsSectionProps {
  isLoading: boolean;
  isError: boolean;
  chartData: ChartPoint[];
  latestPoint: ChartPoint | undefined;
}

export function TrendsSection({ isLoading, isError, chartData, latestPoint }: TrendsSectionProps) {
  return (
    <section className="pt-10 mb-10">
      <SectionHeader title="Trends" tip="Daily aggregates for the selected period" />
      {isLoading ? (
        <Loading />
      ) : isError ? (
        <Err label="trends" />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <ChartCard
            title="Reviews / Day"
            value={latestPoint ? String(latestPoint.review_count) : "—"}
            color="#10b981"
          >
            <AreaChart data={chartData}>
              <XAxis
                dataKey="day"
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                stroke="hsl(var(--border))"
              />
              <YAxis
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                width={30}
                stroke="hsl(var(--border))"
              />
              <Tooltip contentStyle={tooltipStyle} />
              <Area
                type="monotone"
                dataKey="review_count"
                stroke="#10b981"
                fill="#10b981"
                fillOpacity={0.15}
                strokeWidth={1.5}
              />
            </AreaChart>
          </ChartCard>
          <ChartCard
            title="Cost / Day"
            value={latestPoint ? fmt$(latestPoint.total_cost) : "—"}
            color="#3b82f6"
          >
            <AreaChart data={chartData}>
              <XAxis
                dataKey="day"
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                stroke="hsl(var(--border))"
              />
              <YAxis
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                width={40}
                stroke="hsl(var(--border))"
                tickFormatter={(v) => `$${v}`}
              />
              <Tooltip
                contentStyle={tooltipStyle}
                formatter={(v) => [`$${Number(v).toFixed(3)}`, "Cost"]}
              />
              <Area
                type="monotone"
                dataKey="total_cost"
                stroke="#3b82f6"
                fill="#3b82f6"
                fillOpacity={0.15}
                strokeWidth={1.5}
              />
            </AreaChart>
          </ChartCard>
          <ChartCard
            title="Score / Day"
            value={latestPoint ? latestPoint.avg_score.toFixed(1) : "—"}
            color="#10b981"
          >
            <AreaChart data={chartData}>
              <XAxis
                dataKey="day"
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                stroke="hsl(var(--border))"
              />
              <YAxis
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                width={30}
                domain={[0, 10]}
                stroke="hsl(var(--border))"
              />
              <Tooltip contentStyle={tooltipStyle} />
              <Area
                type="monotone"
                dataKey="avg_score"
                stroke="#10b981"
                fill="#10b981"
                fillOpacity={0.15}
                strokeWidth={1.5}
              />
            </AreaChart>
          </ChartCard>
          <ChartCard
            title="Tokens / Day"
            value={latestPoint ? fmtTok(latestPoint.total_tokens) : "—"}
            color="#8b5cf6"
          >
            <AreaChart data={chartData}>
              <XAxis
                dataKey="day"
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                stroke="hsl(var(--border))"
              />
              <YAxis
                tick={{ fontSize: 10, fill: "hsl(var(--muted-foreground))" }}
                width={40}
                stroke="hsl(var(--border))"
                tickFormatter={(v) => fmtTok(Number(v))}
              />
              <Tooltip
                contentStyle={tooltipStyle}
                formatter={(v) => [fmtTok(Number(v)), "Tokens"]}
              />
              <Area
                type="monotone"
                dataKey="total_tokens"
                stroke="#8b5cf6"
                fill="#8b5cf6"
                fillOpacity={0.15}
                strokeWidth={1.5}
              />
            </AreaChart>
          </ChartCard>
        </div>
      )}
    </section>
  );
}
