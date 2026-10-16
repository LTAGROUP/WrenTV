#!/usr/bin/env python3
"""Wren TV account emails: templates, subjects and settings for Supabase Auth.

Every email Supabase sends for Wren TV accounts is defined here, so the hosted
project can be rebuilt from the repository:

    python3 supabase/email/apply.py --render /tmp/wrentv-emails   # preview as HTML files
    python3 supabase/email/apply.py --dry-run                     # print the settings
    SUPABASE_ACCESS_TOKEN=sbp_... python3 supabase/email/apply.py # apply to the project

The access token is a personal token from supabase.com/dashboard/account/tokens
for an account that can manage the project. Email is sent through Brevo, set up in
the dashboard (Authentication > Emails > SMTP Settings); this script leaves SMTP
alone unless SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASS and SMTP_ADMIN_EMAIL are
in the environment.

Without an access token, the Supabase CLI (2.121+, logged in to the account that
owns the project) can push the same thing: --render the emails into a workdir's
supabase/templates/, reference them from [auth.email.template.*] and
[auth.email.notification.*] in its config.toml, then run
`supabase config push --project-ref buycndonhmcafzzcvmgu --workdir <dir>`.
Answer "n" first and read the diff: unset keys fall back to CLI defaults.

Links in the emails go to the web player (watch.wrentv.com/auth/confirm), which
verifies the token_hash itself. Each email also carries the 6-digit code, which
the apps and the web player accept instead of the link.
"""

import argparse
import html
import json
import os
import sys
import urllib.error
import urllib.request

PROJECT_REF = os.environ.get("SUPABASE_PROJECT_REF", "buycndonhmcafzzcvmgu")
SITE_URL = "https://watch.wrentv.com"
REDIRECT_URLS = [
    "https://watch.wrentv.com/**",
    "https://wrentv.com/**",
    "http://localhost:8788/**",
]
LOGO = "https://wrentv.com/icon-192.png"

EMBER, INK, CREAM, MUTED, LINE = "#E04A2A", "#1C1A21", "#F7F1E8", "#6b645e", "#e7dfd3"


def confirm_url(kind):
    return "{{ .SiteURL }}/auth/confirm?token_hash={{ .TokenHash }}&type=" + kind


def layout(title, intro, button=None, code=True, footer="If you didn't ask for this, you can ignore this email."):
    """The shared email shell. Table layout and inline styles, so it renders in every mail client."""
    btn = ""
    if button:
        label, href = button
        btn = f"""
          <tr><td style="padding:8px 32px 8px">
            <a href="{href}" style="display:inline-block;background:{EMBER};color:#ffffff;font-weight:700;font-size:15px;text-decoration:none;padding:12px 22px;border-radius:8px">{label}</a>
          </td></tr>"""
    otp = ""
    if code:
        lead = "Or enter this code:" if button else "Your code:"
        otp = f"""
          <tr><td style="padding:16px 32px 4px;color:{MUTED};font-size:14px">{lead}</td></tr>
          <tr><td style="padding:0 32px 8px">
            <div style="display:inline-block;font-family:SFMono-Regular,Menlo,Consolas,monospace;font-size:28px;font-weight:700;letter-spacing:6px;color:{INK};background:#f3ece1;border-radius:8px;padding:10px 16px">{{{{ .Token }}}}</div>
          </td></tr>
          <tr><td style="padding:0 32px 8px;color:{MUTED};font-size:13px">{"The link and code expire" if button else "It expires"} in 1 hour and work{"" if button else "s"} once.</td></tr>"""
    return f"""<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light"><title>{html.escape(title)}</title></head>
<body style="margin:0;padding:0;background:{CREAM}">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:{CREAM}">
    <tr><td align="center" style="padding:32px 16px">
      <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#ffffff;border:1px solid {LINE};border-radius:14px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:{INK}">
        <tr><td style="padding:28px 32px 8px">
          <img src="{LOGO}" width="40" height="40" alt="Wren TV" style="display:block;border-radius:9px">
        </td></tr>
        <tr><td style="padding:12px 32px 4px;font-size:22px;font-weight:700">{title}</td></tr>
        <tr><td style="padding:4px 32px 16px;font-size:15px;line-height:1.5">{intro}</td></tr>{btn}{otp}
        <tr><td style="padding:20px 32px 28px;color:{MUTED};font-size:13px;line-height:1.5;border-top:1px solid {LINE}">
          {footer}<br>Wren TV &middot; <a href="https://wrentv.com" style="color:{MUTED}">wrentv.com</a>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>
"""


def notice(title, body):
    return layout(
        title, body, code=False,
        footer="If this wasn't you, reset your password at <a href=\"{{ .SiteURL }}\" style=\"color:#6b645e\">watch.wrentv.com</a> right away.",
    )


# (subject, content) per Supabase template. Keys are the Management API's names.
TEMPLATES = {
    "confirmation": (
        "Confirm your Wren TV account",
        layout("Confirm your email", "Welcome to Wren TV. Confirm <b>{{ .Email }}</b> to finish creating your account.",
               ("Confirm email", confirm_url("email"))),
    ),
    "invite": (
        "You're invited to Wren TV",
        layout("You're invited", "You've been invited to Wren TV with <b>{{ .Email }}</b>. Accept to set up your account.",
               ("Accept invite", confirm_url("invite")), code=False),
    ),
    "magic_link": (
        "Your Wren TV sign-in code",
        layout("Sign in to Wren TV", "Use the button or the code below to sign in as <b>{{ .Email }}</b>.",
               ("Sign in", confirm_url("email"))),
    ),
    "recovery": (
        "Reset your Wren TV password",
        layout("Reset your password", "Someone asked to reset the password for <b>{{ .Email }}</b>. Choose a new one here:",
               ("Reset password", confirm_url("recovery"))),
    ),
    "email_change": (
        "Confirm your new Wren TV email",
        layout("Confirm your email change", "Confirm changing your Wren TV email from <b>{{ .Email }}</b> to <b>{{ .NewEmail }}</b>.",
               ("Confirm change", confirm_url("email_change"))),
    ),
    "reauthentication": (
        "Your Wren TV verification code",
        layout("Confirm it's you", "Enter this code to finish what you started in Wren TV."),
    ),
}

# Security notices. Sent after the change happens; they carry no link or code.
NOTIFICATIONS = {
    "password_changed": (
        "Your Wren TV password was changed",
        notice("Password changed", "The password for <b>{{ .Email }}</b> was just changed."),
    ),
    "email_changed": (
        "Your Wren TV email was changed",
        notice("Email changed", "Your Wren TV email was changed from <b>{{ .OldEmail }}</b> to <b>{{ .Email }}</b>."),
    ),
    "identity_linked": (
        "A sign-in method was added to your Wren TV account",
        notice("Sign-in method added", "A new sign-in method ({{ .Provider }}) was linked to <b>{{ .Email }}</b>."),
    ),
    "identity_unlinked": (
        "A sign-in method was removed from your Wren TV account",
        notice("Sign-in method removed", "The {{ .Provider }} sign-in method was removed from <b>{{ .Email }}</b>."),
    ),
    "mfa_factor_enrolled": (
        "Two-factor authentication was turned on",
        notice("Two-factor added", "A {{ .FactorType }} factor was added to <b>{{ .Email }}</b>."),
    ),
    "mfa_factor_unenrolled": (
        "Two-factor authentication was removed",
        notice("Two-factor removed", "A {{ .FactorType }} factor was removed from <b>{{ .Email }}</b>."),
    ),
}


def core_settings():
    s = {
        "site_url": SITE_URL,
        "uri_allow_list": ",".join(REDIRECT_URLS),
        "external_email_enabled": True,
        "mailer_autoconfirm": False,
        "mailer_secure_email_change_enabled": True,
        "mailer_otp_exp": 3600,
        "mailer_otp_length": 6,
    }
    for name, (subject, content) in TEMPLATES.items():
        s[f"mailer_subjects_{name}"] = subject
        s[f"mailer_templates_{name}_content"] = content
    return s


def notification_settings():
    s = {}
    for name, (subject, content) in NOTIFICATIONS.items():
        s[f"mailer_notifications_{name}_enabled"] = True
        s[f"mailer_subjects_{name}_notification"] = subject
        s[f"mailer_templates_{name}_notification_content"] = content
    return s


def smtp_settings():
    keys = ("SMTP_HOST", "SMTP_PORT", "SMTP_USER", "SMTP_PASS", "SMTP_ADMIN_EMAIL")
    if not all(os.environ.get(k) for k in keys):
        return None
    return {
        "smtp_host": os.environ["SMTP_HOST"],
        "smtp_port": os.environ["SMTP_PORT"],
        "smtp_user": os.environ["SMTP_USER"],
        "smtp_pass": os.environ["SMTP_PASS"],
        "smtp_admin_email": os.environ["SMTP_ADMIN_EMAIL"],
        "smtp_sender_name": os.environ.get("SMTP_SENDER_NAME", "Wren TV"),
        "smtp_max_frequency": 60,  # seconds between emails to one address
        "rate_limit_email_sent": int(os.environ.get("SMTP_RATE_PER_HOUR", "100")),
    }


def patch(token, body):
    req = urllib.request.Request(
        f"https://api.supabase.com/v1/projects/{PROJECT_REF}/config/auth",
        data=json.dumps(body).encode(), method="PATCH",
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json", "User-Agent": "wrentv-email-config"},
    )
    try:
        with urllib.request.urlopen(req) as res:
            return res.status, ""
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--render", metavar="DIR", help="write every email as an HTML file and exit")
    ap.add_argument("--dry-run", action="store_true", help="print the settings (minus SMTP password) and exit")
    args = ap.parse_args()

    if args.render:
        os.makedirs(args.render, exist_ok=True)
        for name, (_, content) in {**TEMPLATES, **NOTIFICATIONS}.items():
            with open(os.path.join(args.render, name + ".html"), "w") as f:
                f.write(content)
        print(f"Wrote {len(TEMPLATES) + len(NOTIFICATIONS)} emails to {args.render}")
        return

    smtp = smtp_settings()
    if args.dry_run:
        out = {**core_settings(), **notification_settings(), **(smtp or {})}
        if smtp:
            out["smtp_pass"] = "********"
        json.dump({k: (v if "_content" not in k else f"<{len(v)} chars>") for k, v in out.items()}, sys.stdout, indent=2)
        print()
        return

    token = os.environ.get("SUPABASE_ACCESS_TOKEN")
    if not token:
        sys.exit("Set SUPABASE_ACCESS_TOKEN (supabase.com/dashboard/account/tokens).")

    steps = [("Templates, subjects and redirect URLs", core_settings()), ("Security notifications", notification_settings())]
    if smtp:
        steps.append(("Custom SMTP", smtp))
    else:
        print("SMTP_* not set: leaving SMTP as it is.")
    failed = False
    for label, body in steps:
        status, err = patch(token, body)
        ok = 200 <= status < 300
        failed |= not ok
        print(f"{'ok ' if ok else 'ERR'} {label}" + ("" if ok else f" ({status}): {err}"))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
