// link-device: a signed-in user approves a code shown on a TV.
//
// POST { "code": "ABCD2345", "check": true } with the user's access token
//   → { deviceName, platform } so the page can ask before approving.
// POST { "code": "ABCD2345" } with the user's access token
//   → mints a one-time sign-in token for the user and attaches it to the code;
//     the TV (polling poll_device_link) exchanges it for its own session via
//     POST /auth/v1/verify { type: "magiclink", token_hash }.
import { createClient } from "npm:@supabase/supabase-js@2";

const cors = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Headers": "authorization, x-client-info, apikey, content-type",
  "Access-Control-Allow-Methods": "POST, OPTIONS",
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { ...cors, "Content-Type": "application/json" } });
}

Deno.serve(async (req) => {
  if (req.method === "OPTIONS") return new Response("ok", { headers: cors });
  if (req.method !== "POST") return json({ error: "method not allowed" }, 405);

  const url = Deno.env.get("SUPABASE_URL")!;
  const anon = Deno.env.get("SUPABASE_ANON_KEY")!;
  const service = Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!;

  const authHeader = req.headers.get("Authorization") ?? "";
  const userClient = createClient(url, anon, { global: { headers: { Authorization: authHeader } } });
  const { data: { user }, error: userErr } = await userClient.auth.getUser();
  if (userErr || !user?.email) return json({ error: "Sign in to approve a TV." }, 401);

  let code = "";
  let check = false;
  try {
    const body = await req.json();
    code = String(body.code ?? "").toUpperCase().replace(/[^A-Z0-9]/g, "");
    check = body.check === true;
  } catch {
    return json({ error: "invalid body" }, 400);
  }
  if (!/^[A-Z0-9]{8}$/.test(code)) return json({ error: "That isn't a TV sign-in code." }, 400);

  const admin = createClient(url, service);
  const { data: info } = await admin.rpc("device_link_info", { p_code: code });
  const link = Array.isArray(info) ? info[0] : null;
  if (!link) return json({ error: "This code has expired. Get a new one on the TV." }, 404);
  if (link.claimed) return json({ error: "This code was already used." }, 409);
  if (check) return json({ deviceName: link.device_name, platform: link.platform });

  const { data: gen, error: genErr } = await admin.auth.admin.generateLink({ type: "magiclink", email: user.email });
  if (genErr || !gen?.properties?.hashed_token) return json({ error: "Couldn't create a sign-in for the TV." }, 500);

  const { data: ok, error: apprErr } = await admin.rpc("approve_device_link", {
    p_code: code,
    p_user_id: user.id,
    p_token_hash: gen.properties.hashed_token,
  });
  if (apprErr || !ok) return json({ error: "This code was already used or has expired." }, 409);

  return json({ ok: true, deviceName: link.device_name, platform: link.platform });
});
