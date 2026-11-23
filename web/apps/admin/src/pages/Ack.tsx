import { useState } from "react";
import { Link, useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2Icon, CircleCheckBigIcon, Loader2, TriangleAlertIcon } from "lucide-react";
import { formatDateTime, formatDuration } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Logo } from "../components/Shell";

type AckPage = {
  valid: boolean;
  monitor?: string;
  status?: string;
  since?: string;
  over: boolean;
  who?: string;
  ack?: { at: string };
};

async function call(method: "GET" | "POST", token: string): Promise<{ status: number; data: AckPage & { error?: string; code?: string } }> {
  const res = await fetch(`/api/ack/${encodeURIComponent(token)}`, { method, credentials: "same-origin" });
  return { status: res.status, data: await res.json().catch(() => ({ valid: false, over: false })) };
}

/** The page behind an alert's "Acknowledge" button. Opening it does nothing
 *  on its own (chat apps preview links); the button does. */
export function AckPage() {
  const token = useParams().token ?? "";
  const { data, isLoading, refetch } = useQuery({ queryKey: ["ack-page", token], queryFn: () => call("GET", token), retry: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const acknowledge = async () => {
    setBusy(true);
    setError(null);
    const r = await call("POST", token).catch(() => null);
    setBusy(false);
    if (!r) setError("Couldn't reach Drix. Try again.");
    else if (r.status >= 400 && r.data.code !== "over") setError(r.data.error ?? "Something went wrong");
    void refetch();
  };

  const page = data?.data;
  let body: React.ReactNode;
  if (isLoading) {
    body = <Loader2 className="mx-auto size-5 animate-spin text-muted-foreground" />;
  } else if (!page?.valid) {
    body = <Message icon={<TriangleAlertIcon className="text-muted-foreground" />} title="This link isn't valid" text="It may be from another Drix, or it was cut off. Open the monitor in Drix instead." />;
  } else if (page.over) {
    body = <Message icon={<CircleCheckBigIcon className="text-up" />} title={`${page.monitor} isn't down any more`} text="That outage is over, so there's nothing to acknowledge." />;
  } else if (page.ack) {
    body = (
      <Message
        icon={<CheckCircle2Icon className="text-up" />}
        title={`Acknowledged by ${page.who}`}
        text={`${page.monitor} has been down for ${formatDuration(page.since)}. Reminders and escalations are paused until it recovers.`}
      />
    );
  } else {
    body = (
      <div className="grid gap-4 text-center">
        <div>
          <p className="inline-flex items-center gap-1.5 rounded-full bg-down/10 px-2.5 py-0.5 text-xs font-medium text-down">
            <span className="size-1.5 rounded-full bg-down" /> Down for {formatDuration(page.since)}
          </p>
          <h1 className="mt-3 text-lg font-semibold tracking-tight">{page.monitor}</h1>
          <p className="mt-1 text-[13px] text-muted-foreground">Since {page.since ? formatDateTime(page.since) : "—"}</p>
        </div>
        <p className="text-[13px] text-muted-foreground">Acknowledging tells the team you're on it and stops reminders and escalations for this outage.</p>
        {error && <p className="rounded-md bg-destructive/8 px-3 py-2 text-[13px] text-destructive">{error}</p>}
        <Button className="w-full" disabled={busy} onClick={acknowledge}>
          {busy && <Loader2 className="animate-spin" />}
          Acknowledge
        </Button>
      </div>
    );
  }

  return (
    <div className="flex min-h-dvh items-center justify-center bg-subtle px-4">
      <div className="w-full max-w-[22rem]">
        <div className="mb-6 flex justify-center">
          <Logo className="size-8" />
        </div>
        <div className="rounded-xl border bg-card p-6 shadow-xs">{body}</div>
        <p className="mt-4 text-center text-[13px]">
          <Link to="/" className="text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">
            Open Drix
          </Link>
        </p>
      </div>
    </div>
  );
}

function Message({ icon, title, text }: { icon: React.ReactNode; title: string; text: string }) {
  return (
    <div className="grid justify-items-center gap-2 text-center [&_svg]:size-7">
      {icon}
      <h1 className="mt-1 text-[15px] font-semibold tracking-tight">{title}</h1>
      <p className="text-[13px] text-muted-foreground">{text}</p>
    </div>
  );
}
