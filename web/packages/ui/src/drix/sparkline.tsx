import { cn } from "../lib/utils";

/**
 * Tiny latency sparkline. Values < 0 are failed checks, drawn as red ticks.
 */
export function Sparkline({
  values,
  width = 96,
  height = 24,
  className,
}: {
  values: number[];
  width?: number;
  height?: number;
  className?: string;
}) {
  if (!values || values.length < 2) {
    return <div style={{ width, height }} className={cn("flex items-center", className)}><div className="h-px w-full bg-border" /></div>;
  }
  const ok = values.filter((v) => v >= 0);
  const maxV = Math.max(1, ...ok);
  const minV = Math.min(...(ok.length ? ok : [0]));
  const range = Math.max(1, maxV - minV);
  const step = width / (values.length - 1);
  const pad = 2;
  const y = (v: number) => pad + (height - pad * 2) * (1 - (v - minV) / range);

  let d = "";
  let pen = false;
  values.forEach((v, i) => {
    if (v < 0) {
      pen = false;
      return;
    }
    d += `${pen ? "L" : "M"}${(i * step).toFixed(1)},${y(v).toFixed(1)}`;
    pen = true;
  });

  return (
    <svg width={width} height={height} className={cn("overflow-visible", className)} aria-hidden>
      <path d={d} fill="none" stroke="currentColor" strokeWidth={1.25} strokeLinejoin="round" strokeLinecap="round" className="text-muted-foreground/80" />
      {values.map((v, i) =>
        v < 0 ? <line key={i} x1={i * step} x2={i * step} y1={2} y2={height - 2} className="stroke-down" strokeWidth={1.5} /> : null,
      )}
    </svg>
  );
}
