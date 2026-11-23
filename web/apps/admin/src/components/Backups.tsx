import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CloudIcon, DatabaseBackupIcon, DownloadIcon, Loader2Icon } from "lucide-react";
import { cn, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Badge } from "@drix/ui/components/display";
import { Field, Input, Switch } from "@drix/ui/components/form";
import { api, ApiError, type BackupInfo } from "../api";
import { useCan } from "../lib/roles";

type SectionComponent = React.ComponentType<{ title: string; description: string; children: React.ReactNode }>;

export function formatBytes(n: number) {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

const kindLabel = (name: string) =>
  name.includes("-pre-update") ? "Before update" : name.includes("-pre-restore") ? "Before restore" : name.includes("-manual") ? "From the server" : "";

const when = new Intl.DateTimeFormat(undefined, { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" });

/** Settings → Backups: nightly database backups, kept on the server and optionally uploaded. */
export function BackupsSection({ Section }: { Section: SectionComponent }) {
  const qc = useQueryClient();
  const can = useCan();
  const isOwner = can("owner");
  const { data } = useQuery({ queryKey: ["backups"], queryFn: () => api.get<BackupInfo>("/backups") });
  const [f, setF] = useState({ enabled: true, time: "03:00", keep: 14 });
  const [s3, setS3] = useState({ enabled: false, endpoint: "", region: "", bucket: "", prefix: "", access_key: "", secret: "" });
  const [secretTouched, setSecretTouched] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!data) return;
    setF({ enabled: data.enabled, time: data.time, keep: data.keep });
    setS3({ ...data.s3, secret: "" });
    setSecretTouched(false);
  }, [data]);

  const body = () => ({ ...f, s3: { ...s3, secret: secretTouched ? s3.secret : undefined } });
  const onFieldError = (e: Error) => {
    if (e instanceof ApiError && e.fields) setErrors(e.fields);
    toast.error(e.message);
  };

  const save = useMutation({
    mutationFn: () => api.put<BackupInfo>("/backups/settings", body()),
    onSuccess: (d) => {
      qc.setQueryData(["backups"], d);
      setErrors({});
      toast.success("Backup settings saved");
    },
    onError: onFieldError,
  });
  const test = useMutation({
    mutationFn: () => api.post("/backups/test-s3", body()),
    onSuccess: () => {
      setErrors({});
      toast.success("Connected to the bucket");
    },
    onError: onFieldError,
  });
  const run = useMutation({
    mutationFn: () => api.post<{ warning?: string }>("/backups/run"),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["backups"] });
      if (r.warning) toast.warning(r.warning);
      else toast.success("Backup made");
    },
    onError: (e) => {
      qc.invalidateQueries({ queryKey: ["backups"] });
      toast.error(e.message);
    },
  });

  const st = data?.status;
  const failedLast = !!st?.last_error && (!st.last_at || new Date(st.last_error_at ?? 0).getTime() > new Date(st.last_at).getTime());

  return (
    <Section
      title="Backups"
      description="The database (monitors, history, users and settings) is backed up every night and kept on the server. Add S3-compatible storage to keep copies somewhere else too."
    >
      {data?.unavailable ? (
        <p className="rounded-md border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">{data.unavailable}</p>
      ) : (
        st && (
          <div
            className={cn(
              "flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-[13px]",
              failedLast ? "border-down/40 bg-down/5" : "bg-muted/30",
            )}
          >
            <DatabaseBackupIcon className={cn("mt-0.5 size-4 shrink-0", failedLast ? "text-down" : "text-muted-foreground")} />
            <div className="min-w-0 flex-1">
              {failedLast ? (
                <>
                  <p className="font-medium text-down">Last backup failed {timeAgo(st.last_error_at!)}</p>
                  <p className="mt-0.5 font-mono text-xs break-words text-muted-foreground">{st.last_error}</p>
                </>
              ) : st.last_at ? (
                <>
                  <p className="font-medium">
                    Last backup {timeAgo(st.last_at)} · {formatBytes(st.last_size)}
                  </p>
                  {st.upload_error ? (
                    <p className="mt-0.5 text-xs text-degraded">Kept on the server, but the upload failed: {st.upload_error}</p>
                  ) : (
                    st.uploaded && <p className="mt-0.5 text-xs text-muted-foreground">Also uploaded to {data.s3.bucket}</p>
                  )}
                </>
              ) : (
                <p className="text-muted-foreground">No backups yet. The first one runs at {f.time} UTC, or make one now.</p>
              )}
            </div>
            {isOwner && (
              <Button type="button" size="sm" variant="outline" disabled={run.isPending} onClick={() => run.mutate()}>
                {run.isPending && <Loader2Icon className="animate-spin" />}
                Back up now
              </Button>
            )}
          </div>
        )
      )}

      <div className="grid gap-4">
        <label className="flex items-center gap-2.5 text-[13px]">
          <Switch checked={f.enabled} onCheckedChange={(v) => setF((p) => ({ ...p, enabled: v }))} />
          Back up every night
        </label>
        <div className="grid grid-cols-2 gap-4 sm:max-w-xs">
          <Field label="Time (UTC)" error={errors.time}>
            <Input type="time" value={f.time} disabled={!f.enabled} onChange={(e) => setF((p) => ({ ...p, time: e.target.value }))} />
          </Field>
          <Field label="Backups to keep" error={errors.keep}>
            <Input type="number" min={1} max={365} value={f.keep} onChange={(e) => setF((p) => ({ ...p, keep: Number(e.target.value) }))} />
          </Field>
        </div>
      </div>

      <div className="grid gap-4 rounded-lg border p-4">
        <label className="flex items-center gap-2.5 text-[13px] font-medium">
          <Switch checked={s3.enabled} onCheckedChange={(v) => setS3((p) => ({ ...p, enabled: v }))} />
          <CloudIcon className="size-4 text-muted-foreground" />
          Also upload to S3-compatible storage
        </label>
        {s3.enabled && (
          <>
            <p className="-mt-1 text-xs text-muted-foreground">
              AWS S3, Cloudflare R2, Backblaze B2, MinIO and others. Use a bucket only Drix writes to; the same number of backups is kept there.
            </p>
            <div className="grid gap-4 sm:grid-cols-[1fr_10rem]">
              <Field label="Endpoint" error={errors["s3.endpoint"]} hint="Leave empty for AWS. R2: https://<account>.r2.cloudflarestorage.com">
                <Input value={s3.endpoint} onChange={(e) => setS3((p) => ({ ...p, endpoint: e.target.value }))} placeholder="https://s3.eu-central-1.amazonaws.com" />
              </Field>
              <Field label="Region" error={errors["s3.region"]} hint="R2: auto">
                <Input value={s3.region} onChange={(e) => setS3((p) => ({ ...p, region: e.target.value }))} placeholder="eu-central-1" />
              </Field>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Bucket" error={errors["s3.bucket"]}>
                <Input value={s3.bucket} onChange={(e) => setS3((p) => ({ ...p, bucket: e.target.value }))} placeholder="acme-drix-backups" />
              </Field>
              <Field label="Folder (optional)" error={errors["s3.prefix"]}>
                <Input value={s3.prefix} onChange={(e) => setS3((p) => ({ ...p, prefix: e.target.value }))} placeholder="drix" />
              </Field>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Access key" error={errors["s3.access_key"]}>
                <Input value={s3.access_key} autoComplete="off" onChange={(e) => setS3((p) => ({ ...p, access_key: e.target.value }))} />
              </Field>
              <Field
                label="Secret key"
                error={errors["s3.secret"]}
                hint={!data?.can_store_secrets ? "Set SECRET_KEY on the server to store it." : data?.s3.has_secret && !secretTouched ? "Saved. Type to replace it." : undefined}
              >
                <Input
                  type="password"
                  autoComplete="new-password"
                  value={s3.secret}
                  placeholder={data?.s3.has_secret ? "••••••••" : ""}
                  onChange={(e) => {
                    setSecretTouched(true);
                    setS3((p) => ({ ...p, secret: e.target.value }));
                  }}
                />
              </Field>
            </div>
            <div>
              <Button type="button" variant="outline" size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
                {test.isPending && <Loader2Icon className="animate-spin" />}
                Test connection
              </Button>
            </div>
          </>
        )}
      </div>

      <div>
        <Button type="button" onClick={() => save.mutate()} disabled={save.isPending}>
          {save.isPending && <Loader2Icon className="animate-spin" />}
          Save backup settings
        </Button>
      </div>

      {!!data?.files.length && (
        <div className="grid gap-2">
          <p className="text-xs font-medium text-muted-foreground">On the server</p>
          <ul className="divide-y rounded-lg border">
            {data.files.map((b) => (
              <li key={b.name} className="flex items-center gap-3 px-3 py-2 text-[13px]">
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{when.format(new Date(b.created_at))}</span>
                  <span className="block truncate font-mono text-[11px] text-muted-foreground">{b.name}</span>
                </span>
                {kindLabel(b.name) && <Badge variant="muted">{kindLabel(b.name)}</Badge>}
                {b.uploaded && <Badge variant="muted">Uploaded</Badge>}
                <span className="w-16 text-right text-xs tabular-nums text-muted-foreground">{formatBytes(b.size)}</span>
                {isOwner && (
                  <Button asChild variant="ghost" size="icon-sm" aria-label={`Download ${b.name}`}>
                    <a href={`/api/admin/backups/files/${encodeURIComponent(b.name)}`} download>
                      <DownloadIcon />
                    </a>
                  </Button>
                )}
              </li>
            ))}
          </ul>
          <p className="text-xs text-muted-foreground">
            To restore one, run <code className="font-mono">sudo /opt/drix/install.sh restore {data.files[0].name}</code> on the server.
          </p>
        </div>
      )}
    </Section>
  );
}
