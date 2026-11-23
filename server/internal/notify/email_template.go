package notify

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// emailDoc describes one email; renderEmail turns it into HTML. Every
// field is plain text and escaped on render, except Footer, which is
// built by this package from escaped parts.
type emailDoc struct {
	Preheader    string // inbox preview line
	Brand        emailBrand
	Status       string // small label above the title, e.g. "Down"
	StatusColor  string // colour of the dot before it
	Title        string
	Paragraphs   []string
	Code         string // monospace box, e.g. a check error
	Rows         [][2]string
	Button       *emailButton
	Secondary    *emailButton // an outlined button under the main one
	LinkFallback bool         // show the button's URL as text under it
	Note         string       // small grey text at the end of the card
	Footer       string       // HTML
}

type emailBrand struct {
	Name    string
	LogoURL string
}

type emailButton struct{ Label, URL string }

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

const (
	emailFont = `-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif`
	emailMono = `ui-monospace,SFMono-Regular,Menlo,Consolas,'Liberation Mono',monospace`
)

// font returns inline font declarations (no shorthand: older clients drop it).
func font(size, line, weight int) string {
	return fmt.Sprintf("font-family:%s;font-size:%dpx;line-height:%dpx;font-weight:%d;", emailFont, size, line, weight)
}

// The email is a single dark card with no page background: the mail
// app's own background shows around it, and everything (brand, content,
// footer) lives inside the card so it reads on light and dark apps alike.
// The color-scheme meta tells apps not to invert it. Everything that
// matters is inline, because several clients (Gmail) strip <style>.
const emailStyle = `<style>
body{margin:0;padding:0;-webkit-text-size-adjust:100%;}
a[x-apple-data-detectors]{color:inherit !important;text-decoration:none !important;}
@media (max-width:560px){.x-pad{padding:28px 24px !important;}.x-outer{padding:16px 8px !important;}}
</style>`

// Palette.
const (
	eCard    = "#0a0a0a"
	eBorder  = "#1f1f1f"
	eLine    = "#1a1a1a"
	eTitle   = "#fafafa"
	eStrong  = "#ededed"
	eText    = "#a1a1aa"
	eMuted   = "#71717a"
	eSurface = "#111111"
)

// Status colours are brightened a step so they read well on black.
// Maintenance (blue elsewhere) is a light grey here: no blue in emails.
var emailDot = map[string]string{
	"#dc2626": "#ef4444", "#16a34a": "#22c55e", "#d97706": "#f59e0b", "#2563eb": "#d4d4d8", "#71717a": "#a1a1aa",
}

func renderEmail(d emailDoc) string {
	esc := html.EscapeString
	multiline := func(s string) string { return strings.ReplaceAll(esc(strings.TrimSpace(s)), "\n", "<br>") }
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`)
	w(`<meta name="color-scheme" content="light dark"><meta name="supported-color-schemes" content="light dark"><title>%s</title>%s</head>`, esc(d.Title), emailStyle)
	w(`<body style="margin:0;padding:0;">`)
	if d.Preheader != "" {
		// Hidden preview text; the padding keeps body text out of the preview.
		w(`<div style="display:none;font-size:1px;line-height:1px;max-height:0;max-width:0;overflow:hidden;opacity:0;color:transparent;mso-hide:all;">%s%s</div>`,
			esc(d.Preheader), strings.Repeat("&#847;&zwnj;&nbsp;", 40))
	}
	w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0"><tr><td class="x-outer" align="center" style="padding:40px 16px;">`)
	// Outlook for Windows ignores max-width: give it a fixed-width wrapper.
	w(`<!--[if mso]><table role="presentation" width="520" align="center" cellpadding="0" cellspacing="0" border="0"><tr><td><![endif]-->`)
	w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="max-width:520px;">`)

	// The card; everything is inside it.
	w(`<tr><td class="x-pad" bgcolor="%s" style="background-color:%s;border:1px solid %s;border-radius:14px;padding:40px;text-align:left;">`, eCard, eCard, eBorder)

	// Logo at the top of the card, when the status page has one (no name).
	if strings.HasPrefix(d.Brand.LogoURL, "https://") || strings.HasPrefix(d.Brand.LogoURL, "http://") {
		w(`<p style="margin:0 0 32px;"><img src="%s" alt="%s" height="22" style="height:22px;width:auto;border:0;"></p>`, esc(d.Brand.LogoURL), esc(d.Brand.Name))
	}
	if d.Status != "" {
		dot := d.StatusColor
		if bright, ok := emailDot[strings.ToLower(dot)]; ok {
			dot = bright
		}
		if !hexColor.MatchString(dot) {
			dot = eMuted
		}
		// A small pill: coloured dot + label.
		w(`<p style="margin:0 0 20px;"><span style="display:inline-block;padding:4px 10px 4px 9px;border:1px solid #262626;border-radius:999px;background-color:%s;%scolor:#d4d4d8;">`, eSurface, font(12, 18, 500))
		w(`<span style="display:inline-block;width:6px;height:6px;border-radius:3px;background-color:%s;margin-right:7px;vertical-align:1px;"></span>%s</span></p>`, dot, esc(d.Status))
	}
	w(`<h1 style="margin:0;%scolor:%s;letter-spacing:-0.02em;">%s</h1>`, font(22, 30, 600), eTitle, esc(d.Title))
	for _, p := range d.Paragraphs {
		if strings.TrimSpace(p) != "" {
			w(`<p style="margin:12px 0 0;%scolor:%s;">%s</p>`, font(14, 24, 400), eText, multiline(p))
		}
	}
	if strings.TrimSpace(d.Code) != "" {
		w(`<div style="margin:24px 0 0;padding:14px 16px;background-color:%s;border:1px solid %s;border-radius:8px;font-family:%s;font-size:13px;line-height:20px;color:#e4e4e7;word-break:break-word;">%s</div>`,
			eSurface, eBorder, emailMono, multiline(d.Code))
	}
	if len(d.Rows) > 0 {
		w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="margin-top:24px;border-collapse:collapse;">`)
		for _, r := range d.Rows {
			w(`<tr><td style="padding:12px 16px 12px 0;border-top:1px solid %s;width:104px;vertical-align:top;%scolor:%s;">%s</td>`, eLine, font(13, 20, 400), eMuted, esc(r[0]))
			w(`<td style="padding:12px 0;border-top:1px solid %s;vertical-align:top;%scolor:%s;word-break:break-word;">%s</td></tr>`, eLine, font(13, 20, 400), eStrong, multiline(r[1]))
		}
		w(`</table>`)
	}
	if d.Button != nil && d.Button.URL != "" {
		// A full-width white button with black text, centred.
		w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="margin-top:32px;"><tr><td align="center" bgcolor="#fafafa" style="border-radius:8px;background-color:#fafafa;">`)
		w(`<a href="%s" style="display:block;padding:12px 20px;%scolor:#0a0a0a;text-decoration:none;text-align:center;border-radius:8px;">%s</a></td></tr></table>`,
			esc(d.Button.URL), font(14, 20, 600), esc(d.Button.Label))
		if d.LinkFallback {
			w(`<p style="margin:24px 0 0;%scolor:%s;">Or copy this link into your browser:<br><a href="%s" style="color:%s;word-break:break-all;">%s</a></p>`,
				font(12, 18, 400), eMuted, esc(d.Button.URL), eText, esc(d.Button.URL))
		}
	}
	if d.Secondary != nil && d.Secondary.URL != "" {
		w(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="margin-top:12px;"><tr><td align="center" style="border-radius:8px;border:1px solid %s;">`, eLine)
		w(`<a href="%s" style="display:block;padding:11px 20px;%scolor:%s;text-decoration:none;text-align:center;border-radius:8px;">%s</a></td></tr></table>`,
			esc(d.Secondary.URL), font(14, 20, 600), eStrong, esc(d.Secondary.Label))
	}
	if d.Note != "" {
		w(`<p style="margin:24px 0 0;%scolor:%s;">%s</p>`, font(13, 21, 400), eMuted, multiline(d.Note))
	}
	// Footer at the bottom of the card, below a thin line.
	if d.Footer != "" {
		w(`<p style="margin:24px 0 0;padding-top:20px;border-top:1px solid %s;%scolor:%s;">%s</p>`, eLine, font(12, 20, 400), eMuted, d.Footer)
	}
	w(`</td></tr>`)
	w(`</table><!--[if mso]></td></tr></table><![endif]--></td></tr></table></body></html>`)
	return b.String()
}

// TestEmail is the "Send test" email from Settings → Email.
func TestEmail(to, sentAt string) Email {
	d := emailDoc{
		Preheader: "Your SMTP settings work.",
		Brand:     emailBrand{Name: "Drix Status"},
		Status:    "Test", StatusColor: "#16a34a",
		Title:      "Email is working",
		Paragraphs: []string{"This is a test from Drix Status. Your SMTP settings are correct, so alerts, team invites and subscriber updates will be delivered."},
		Rows:       [][2]string{{"Sent to", to}, {"Sent at", sentAt}},
		Footer:     "Sent from Settings → Email in Drix Status.",
	}
	return Email{To: []string{to}, Subject: "Drix Status test email",
		Text: "Email is working\n\nThis is a test from Drix Status. Your SMTP settings are correct.\n", HTML: renderEmail(d)}
}
