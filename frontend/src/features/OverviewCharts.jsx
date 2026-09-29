import React, { useEffect, useRef, useState } from "react";

export const trafficSeries = [
  { key: "requests", label: "访问记录", color: "var(--overview-blue)" },
  { key: "deliveries", label: "提示词交付", color: "var(--overview-purple)" },
  { key: "downloads", label: "文件下载", color: "var(--overview-amber)" },
  { key: "reports", label: "回传接收", color: "var(--overview-green)" },
];
export const exactNumber = (n) =>
  n == null ? "—" : Number(n).toLocaleString("zh-CN");
export function compactNumber(n) {
  if (n == null) return "—";
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return exactNumber(n);
}
export function ChartLegend({ series }) {
  return (
    <div className="overview-legend">
      {series.map((s) => (
        <span key={s.key}>
          <i style={{ background: s.color }} />
          {s.label}
        </span>
      ))}
    </div>
  );
}
export function DailyChart({
  data,
  series,
  title,
  stacked = false,
  emptyLabel = "当前范围暂无记录",
}) {
  const host = useRef(null);
  const [width, setWidth] = useState(600);
  const [selected, setSelected] = useState("");
  useEffect(() => {
    const observer = new ResizeObserver(([entry]) =>
      setWidth(Math.max(280, entry.contentRect.width)),
    );
    observer.observe(host.current);
    return () => observer.disconnect();
  }, []);
  const height = 262,
    left = 48,
    bottom = 32,
    top = 16;
  const chartWidth = width - left - 12,
    chartHeight = height - bottom - top;
  const max = Math.max(
    0,
    ...data.map((d) =>
      stacked
        ? series.reduce((n, s) => n + d[s.key], 0)
        : Math.max(...series.map((s) => d[s.key])),
    ),
  );
  const magnitude = 10 ** Math.floor(Math.log10(Math.max(1, max / 4)));
  const step =
    [1, 2, 5, 10].map((n) => n * magnitude).find((n) => n >= max / 4) || 1;
  const ceiling = max ? Math.ceil(max / step) * step : 4;
  const tickCount = ceiling / step;
  const cell = chartWidth / Math.max(1, data.length);
  const barWidth = Math.min(36, cell * 0.7);
  const tickEvery = Math.max(1, Math.ceil(data.length / (width < 440 ? 4 : 7)));
  const active = data.find((d) => d.date === selected);
  const detail = (d) =>
    `${d.date} · ${series.map((s) => `${s.label} ${exactNumber(d[s.key])}`).join(" · ")}`;
  return (
    <div className="overview-daily-chart" ref={host}>
      <ChartLegend series={series} />
      <svg
        width="100%"
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        role="group"
        aria-label={title}
      >
        {Array.from({ length: tickCount + 1 }, (_, i) => i).map((i) => {
          const y = top + chartHeight * (1 - i / tickCount);
          return (
            <g key={i}>
              <line
                className="overview-gridline"
                x1={left}
                x2={width - 12}
                y1={y}
                y2={y}
              />
              <text
                className="overview-axis"
                x={left - 9}
                y={y + 4}
                textAnchor="end"
              >
                {compactNumber(step * i)}
              </text>
            </g>
          );
        })}
        {data.map((d, i) => {
          const x = left + cell * i + cell / 2;
          let accumulated = 0;
          return (
            <g
              key={d.date}
              role="button"
              tabIndex={0}
              aria-label={detail(d)}
              aria-pressed={selected === d.date}
              onMouseEnter={() => setSelected(d.date)}
              onFocus={() => setSelected(d.date)}
              onClick={() => setSelected(d.date)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  setSelected(d.date);
                }
              }}
            >
              <title>{detail(d)}</title>
              <rect
                x={left + cell * i}
                y={top}
                width={cell}
                height={chartHeight}
                fill={
                  selected === d.date ? "var(--overview-hover)" : "transparent"
                }
              />
              {series.map((s, j) => {
                const value = d[s.key],
                  h = (chartHeight * value) / ceiling;
                const y = top + chartHeight - h - (stacked ? accumulated : 0);
                accumulated += h;
                return (
                  <rect
                    key={s.key}
                    x={
                      stacked
                        ? x - barWidth / 2
                        : x - barWidth / 2 + (j * barWidth) / series.length
                    }
                    y={y}
                    width={
                      stacked
                        ? barWidth
                        : Math.max(1, barWidth / series.length - 1)
                    }
                    height={h}
                    fill={s.color}
                  />
                );
              })}
              {(i % tickEvery === 0 ||
                (i === data.length - 1 && i % tickEvery > tickEvery / 2)) && (
                <text
                  className="overview-axis"
                  x={x}
                  y={height - 8}
                  textAnchor="middle"
                >
                  {d.date.slice(5)}
                </text>
              )}
            </g>
          );
        })}
        {!max && (
          <text
            className="overview-empty-chart"
            x={left + chartWidth / 2}
            y={top + chartHeight / 2}
            textAnchor="middle"
          >
            {emptyLabel}
          </text>
        )}
      </svg>
      <p className="overview-chart-detail" aria-live="polite">
        {active ? detail(active) : "点击柱形查看当天明细"}
      </p>
      <details className="overview-daily-data">
        <summary>查看每日数据</summary>
        <div className="table-scroll">
          <table>
            <caption className="sr-only">{title}</caption>
            <thead>
              <tr>
                <th>日期</th>
                {series.map((s) => (
                  <th key={s.key}>{s.label}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.map((d) => (
                <tr key={d.date}>
                  <td>{d.date}</td>
                  {series.map((s) => (
                    <td key={s.key}>{exactNumber(d[s.key])}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}
