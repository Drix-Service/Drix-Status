import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { BellIcon, CheckCircle2Icon, Loader2Icon, RssIcon, XIcon } from "lucide-react";
import { Button } from "@drix/ui/components/button";
import { Input } from "@drix/ui/components/form";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@drix/ui/components/overlay";
import { pageApi } from "./api";
import { useI18n } from "./i18n";

/** "Subscribe" button with an email + RSS/Atom dialog. */
export function SubscribeButton({ slug, pageName }: { slug?: string; pageName?: string }) {
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [state, setState] = useState<"idle" | "sending" | "sent" | "error">("idle");
  const [error, setError] = useState("");
  const base = pageApi(slug);
  const { m, t, tx } = useI18n();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setState("sending");
    try {
      const res = await fetch(`${base}/subscribe`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email }) });
      if (!res.ok) {
        const d = await res.json().catch(() => ({}));
        throw new Error(d.error ?? m.somethingWrong);
      }
      setState("sent");
    } catch (err) {
      setError(err instanceof Error ? err.message : m.somethingWrong);
      setState("error");
    }
  };

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => { setOpen(true); setState("idle"); }}>
        <BellIcon /> {m.subscribe}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{m.getUpdates}</DialogTitle>
            <DialogDescription>{t(m.getUpdatesDesc, { page: pageName ?? m.thisPage })}</DialogDescription>
          </DialogHeader>
          {state === "sent" ? (
            <div className="flex items-start gap-2.5 rounded-lg border border-up/25 bg-up/[0.06] p-3 text-[13px]">
              <CheckCircle2Icon className="mt-px size-4 shrink-0 text-up" />
              <p>{tx(m.checkInbox, { email: <b>{email}</b> })}</p>
            </div>
          ) : (
            <form onSubmit={submit} className="flex gap-2">
              <Input type="email" required autoFocus placeholder="you@example.com" value={email} onChange={(e) => setEmail(e.target.value)} aria-label={m.emailAddress} />
              <Button type="submit" disabled={state === "sending"}>
                {state === "sending" && <Loader2Icon className="animate-spin" />}
                {m.subscribe}
              </Button>
            </form>
          )}
          {state === "error" && <p className="-mt-2 text-xs text-destructive">{error}</p>}
          <div className="flex items-center gap-3 border-t pt-3 text-[13px] text-muted-foreground">
            <RssIcon className="size-4" />
            <span>{m.orFeed}</span>
            <a className="font-medium text-foreground hover:underline" href={`${base}/feed.atom`}>Atom</a>
            <a className="font-medium text-foreground hover:underline" href={`${base}/feed.rss`}>RSS</a>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

/** Shows the result of a confirm/unsubscribe link (?subscription=…). */
export function SubscriptionNotice() {
  const [params, setParams] = useSearchParams();
  const key = params.get("subscription");
  const [msg, setMsg] = useState<string | null>(null);
  const { m } = useI18n();
  const notices: Record<string, string> = { confirmed: m.noticeConfirmed, unsubscribed: m.noticeUnsubscribed, invalid: m.noticeInvalid };
  useEffect(() => {
    if (key && notices[key]) {
      setMsg(notices[key]);
      params.delete("subscription");
      setParams(params, { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, params, setParams]);
  if (!msg) return null;
  return (
    <div className="mb-4 flex items-start gap-2.5 rounded-xl border bg-card px-4 py-3 text-[13px] shadow-xs">
      <CheckCircle2Icon className="mt-px size-4 shrink-0 text-up" />
      <p className="flex-1">{msg}</p>
      <button onClick={() => setMsg(null)} aria-label={m.dismiss} className="text-muted-foreground hover:text-foreground">
        <XIcon className="size-4" />
      </button>
    </div>
  );
}
