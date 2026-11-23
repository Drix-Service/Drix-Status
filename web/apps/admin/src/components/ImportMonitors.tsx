import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { DownloadIcon, FileUpIcon, Loader2Icon } from "lucide-react";
import { cn } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Badge } from "@drix/ui/components/display";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@drix/ui/components/overlay";
import { api, ApiError } from "../api";
import { keys } from "../lib/queries";

type ImportRow = { line: number; name: string; type: string; target: string; group: string; status: "new" | "skip" | "error"; error?: string; field?: string };
type ImportResult = { rows: ImportRow[]; new: number; skipped: number; errors: number; new_groups: string[]; created: number };

const TEMPLATE = `name,type,target,group,interval_s,retries,degraded_ms,keyword,record_type,resolver,assertions,grace_s
Homepage,http,https://example.com,Website,60,1,1500,Example Domain,,,,
API health,json,https://api.example.com/health,API,60,1,1000,,,,status equals ok,
Database,tcp,db.internal:5432,,60,2,,,,,,
Server,ping,203.0.113.10,,60,1,,,,,,
Mail MX,dns,example.com,,300,1,,,MX,1.1.1.1,,
Nightly backup,push,,,86400,0,,,,,,3600
`;

function downloadTemplate() {
  const url = URL.createObjectURL(new Blob([TEMPLATE], { type: "text/csv" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = "drix-monitors-template.csv";
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** Import monitors from a CSV file: pick a file, check the preview, import. */
export function ImportMonitorsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<{ name: string; text: string } | null>(null);
  const [preview, setPreview] = useState<ImportResult | null>(null);
  const [fileError, setFileError] = useState("");
  const [dragging, setDragging] = useState(false);

  const reset = () => {
    setFile(null);
    setPreview(null);
    setFileError("");
  };

  const check = useMutation({
    mutationFn: (text: string) => api.post<ImportResult>("/monitors/import", { csv: text, dry_run: true }),
    onSuccess: setPreview,
    onError: (e) => setFileError(e.message),
  });

  const run = useMutation({
    mutationFn: () => api.post<ImportResult>("/monitors/import", { csv: file?.text ?? "", dry_run: false }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: keys.monitors });
      qc.invalidateQueries({ queryKey: keys.groups });
      toast.success(`${r.created} monitor${r.created === 1 ? "" : "s"} imported`, {
        description: r.new_groups.length ? `New groups: ${r.new_groups.join(", ")}` : undefined,
      });
      onOpenChange(false);
      reset();
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "Import failed"),
  });

  const load = async (f: File | undefined) => {
    if (!f) return;
    setPreview(null);
    setFileError("");
    if (f.size > 1 << 20) {
      setFileError("The file is larger than 1 MB.");
      return;
    }
    const text = await f.text();
    setFile({ name: f.name, text });
    check.mutate(text);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) reset();
      }}
    >
      <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Import monitors</DialogTitle>
          <DialogDescription>Add many monitors at once from a CSV file. You'll see a preview before anything is created.</DialogDescription>
        </DialogHeader>

        <div
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            load(e.dataTransfer.files[0]);
          }}
          className={cn(
            "flex min-w-0 items-center gap-3 rounded-lg border border-dashed px-4 py-3 transition-colors",
            dragging && "border-foreground/40 bg-muted/40",
          )}
        >
          <FileUpIcon className="size-4 shrink-0 text-muted-foreground" />
          <p className="min-w-0 flex-1 truncate text-[13px]">
            {file ? <span className="font-medium">{file.name}</span> : <span className="text-muted-foreground">Drop a .csv file here, or choose one</span>}
          </p>
          <input ref={input} type="file" accept=".csv,text/csv" className="hidden" onChange={(e) => load(e.target.files?.[0] ?? undefined)} />
          <Button type="button" variant="outline" size="sm" onClick={() => input.current?.click()}>
            {file ? "Choose another" : "Choose file"}
          </Button>
        </div>

        {check.isPending && (
          <p className="flex items-center gap-2 text-[13px] text-muted-foreground">
            <Loader2Icon className="size-3.5 animate-spin" /> Checking the file…
          </p>
        )}
        {fileError && <p className="rounded-lg border border-down/30 bg-down/5 px-3 py-2 text-[13px] text-down">{fileError}</p>}

        {preview && (
          <>
            <p className="text-[13px] text-muted-foreground">
              <span className="font-medium text-foreground">{preview.new} new</span>
              {preview.skipped > 0 && <> · {preview.skipped} already exist</>}
              {preview.errors > 0 && <span className="text-down"> · {preview.errors} with errors</span>}
              {preview.new_groups.length > 0 && <> · new groups: {preview.new_groups.join(", ")}</>}
            </p>
            <div className="max-h-[45vh] min-w-0 overflow-auto rounded-lg border">
              <table className="w-full text-[12.5px]">
                <thead className="sticky top-0 bg-muted/80 backdrop-blur">
                  <tr className="text-left text-xs text-muted-foreground">
                    <th className="px-3 py-2 font-medium">Line</th>
                    <th className="px-3 py-2 font-medium">Name</th>
                    <th className="px-3 py-2 font-medium">Type</th>
                    <th className="px-3 py-2 font-medium">Target</th>
                    <th className="px-3 py-2 font-medium">Group</th>
                    <th className="px-3 py-2 font-medium">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y">
                  {preview.rows.map((r) => (
                    <tr key={r.line} className={cn("align-top", r.status === "skip" && "text-muted-foreground")}>
                      <td className="px-3 py-2 tabular-nums text-muted-foreground">{r.line}</td>
                      <td className="max-w-40 truncate px-3 py-2 font-medium">{r.name || "—"}</td>
                      <td className="px-3 py-2 uppercase">{r.type || "—"}</td>
                      <td className="max-w-56 truncate px-3 py-2 font-mono text-[11.5px]">{r.target || "—"}</td>
                      <td className="max-w-32 truncate px-3 py-2">{r.group || <span className="text-muted-foreground">Private</span>}</td>
                      <td className="px-3 py-2">
                        {r.status === "new" && <Badge variant="up">New</Badge>}
                        {r.status === "skip" && <Badge variant="muted">{r.error}</Badge>}
                        {r.status === "error" && (
                          <div className="grid gap-1">
                            <Badge variant="down" className="w-fit">Error</Badge>
                            <span className="text-[11.5px] text-down">{r.error}</span>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}

        <DialogFooter className="sm:justify-between">
          <Button type="button" variant="ghost" size="sm" onClick={downloadTemplate}>
            <DownloadIcon /> Download template
          </Button>
          <div className="flex gap-2">
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="button" disabled={!preview || preview.errors > 0 || preview.new === 0 || run.isPending} onClick={() => run.mutate()}>
              {run.isPending && <Loader2Icon className="animate-spin" />}
              {preview && preview.errors > 0 ? "Fix errors to import" : `Import ${preview?.new ?? ""} monitor${preview?.new === 1 ? "" : "s"}`}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
