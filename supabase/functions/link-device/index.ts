// link-device: a signed-in user approves a code shown on a new device.
//
// POST { "code": "ABC123" } with the user's access token.
// Mints a one-time sign-in token for the user and attaches it to the code;
// the new device (polling poll_device_link) exchanges it for its own session
// via POST /auth/v1/verify { type: "magiclink", token_hash }.
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
  if (userErr || !user?.email) return json({ error: "not signed in" }, 401);

  let code = "";
  try {
    code = String((await req.json()).code ?? "").trim().toUpperCase();
  } catch {
    return json({ error: "invalid body" }, 400);
  }
  if (!/^[A-Z0-9]{6}$/.test(code)) return json({ error: "invalid code" }, 400);

  const admin = createClient(url, service);
  const { data: info } = await admin.rpc("device_link_info", { p_code: code });
  const link = Array.isArray(info) ? info[0] : null;
  if (!link) return json({ error: "code not found or expired" }, 404);
  if (link.claimed) return json({ error: "code already used" }, 409);

  const { data: gen, error: genErr } = await admin.auth.admin.generateLink({ type: "magiclink", email: user.email });
  if (genErr || !gen?.properties?.hashed_token) return json({ error: "could not create sign-in token" }, 500);

  const { data: ok, error: apprErr } = await admin.rpc("approve_device_link", {
    p_code: code,
    p_user_id: user.id,
    p_token_hash: gen.properties.hashed_token,
  });
  if (apprErr || !ok) return json({ error: "code already used or expired" }, 409);

  return json({ ok: true, deviceName: link.device_name, platform: link.platform });
});
